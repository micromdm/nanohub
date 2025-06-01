// Package dmadapter adapts KMFDDM directly to NanoMDM.
package ddmadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jessepeterson/kmfddm/ddm"
	"github.com/jessepeterson/kmfddm/jsonpath"
	"github.com/jessepeterson/kmfddm/logkeys"
	"github.com/jessepeterson/kmfddm/storage"
	"github.com/micromdm/nanolib/log"
	"github.com/micromdm/nanolib/log/ctxlog"
	"github.com/micromdm/nanomdm/mdm"
)

// ErrUnknownDMEndpoint occurs when an unknown "Endpoint" field value
// is in the DeclarativeManagement check-in message.
var ErrUnknownDMEndpoint = errors.New("unknown DM endpoint in check-in")

type ctxMux struct{}
type ctxStatusReport struct{}

// ContextStatusReport retrieves status report from ctx and returns it.
// If no status report is present in ctx then a new one will be returned
// using raw with out.
func ContextStatusReport(ctx context.Context, raw []byte) (out context.Context, status *ddm.StatusReport) {
	status, ok := ctx.Value(ctxStatusReport{}).(*ddm.StatusReport)
	if !ok || status == nil {
		status = &ddm.StatusReport{Raw: raw}
		out = context.WithValue(ctx, ctxStatusReport{}, status)
	} else {
		out = ctx
	}
	return
}

// ContextJSONMux retreives the [jsonpath.PathMux] from ctx and returns it.
// If no mux is present in ctx then a new one will be returned with out.
func ContextJSONMux(ctx context.Context) (out context.Context, mux *jsonpath.PathMux) {
	mux, ok := ctx.Value(ctxMux{}).(*jsonpath.PathMux)
	if !ok || mux == nil {
		mux = jsonpath.NewPathMux()
		out = context.WithValue(ctx, ctxMux{}, mux)
	} else {
		out = ctx
	}
	return
}

// StatusIDFns generate IDs for status reports.
type StatusIDFn func(*mdm.Request, *ddm.StatusReport) (string, error)

// DMAdapter adapts KMFDDM to NanoMDM.
type DMAdapter struct {
	logger           log.Logger
	declarationStore storage.EnrollmentDeclarationStorage
	statusStore      storage.StatusStorer
	statusIDFn       StatusIDFn
}

// Options configure the adapter.
type Option func(*DMAdapter) error

// WithLogger tells the adapter to log to logger.
func WithLogger(logger log.Logger) Option {
	if logger == nil {
		panic("nil logger")
	}

	return func(dma *DMAdapter) error {
		dma.logger = logger
		return nil
	}
}

// WithStatusIDFn sets the status report ID generator function.
func WithStatusIDFn(f StatusIDFn) Option {
	return func(dma *DMAdapter) error {
		dma.statusIDFn = f
		return nil
	}
}

// WithStatusStore configures storage for the built-in status storage.
func WithStatusStore(s storage.StatusStorer) Option {
	return func(dma *DMAdapter) error {
		dma.statusStore = s
		return nil
	}
}

// New creates a new KMFDDM to NanoMDM adapter.
func New(declarationStore storage.EnrollmentDeclarationStorage, opts ...Option) (*DMAdapter, error) {
	if declarationStore == nil {
		panic("nil declaration store")
	}

	a := &DMAdapter{
		declarationStore: declarationStore,
		logger:           log.NopLogger,
	}

	for _, opt := range opts {
		if err := opt(a); err != nil {
			return nil, err
		}
	}

	return a, nil
}

// handleStatus handles DM status updates from the client.
func (dma *DMAdapter) handleStatus(r *mdm.Request, msg *mdm.DeclarativeManagement) error {
	// get our mux from the context (or make a new one)
	ctx, mux := ContextJSONMux(r.Context())

	// get the status report from the context (or make a new one)
	ctx, status := ContextStatusReport(ctx, msg.Data)

	// register the default handlers
	ddm.RegisterStatusHandlers(mux, status)

	unhandled, err := ddm.ParseStatusUsingMux(status.Raw, mux)
	if err != nil {
		return fmt.Errorf("parsing status: %w", err)
	}

	logger := ctxlog.Logger(ctx, dma.logger)

	for _, path := range unhandled {
		// log the unhandled status paths
		// these are the root paths the jsonpath muxer did not handle.
		logger.Debug(
			logkeys.Message, "unhandled status path",
			"path", path,
		)
	}

	if dma.statusIDFn != nil {
		// if we have a status ID generator, run it to get the string ID
		// for logging and storage keys
		status.ID, err = dma.statusIDFn(r, status)
		logger = logger.With("status_id", status.ID)
		if err != nil {
			logger.Info("msg", "generate status id", "err", err)
		}
	}

	// status report logging (post-parse)
	logger = logger.With(
		logkeys.DeclarationCount, len(status.Declarations),
		logkeys.ErrorCount, len(status.Errors),
		logkeys.ValueCount, len(status.Values),
	)

	if dma.statusStore == nil {
		// skip storing the report entirely.
		// this still allows for any custom parsers to run.
		return nil
	}

	err = dma.statusStore.StoreDeclarationStatus(ctx, r.ID, status)
	if err != nil {
		// log the error with our additional context
		logger.Info("msg", "storing status", "err", err)
		return fmt.Errorf("storing status: %w", err)
	}

	logger.Debug("msg", "stored status")
	return nil
}

// handleTokens handles the retrieval of DM client tokens.
func (dma *DMAdapter) handleTokens(r *mdm.Request) ([]byte, error) {
	ret, err := dma.declarationStore.RetrieveTokensJSON(r.Context(), r.ID)
	if err != nil {
		return ret, fmt.Errorf("retrieving tokens: %w", err)
	}

	ctxlog.Logger(r.Context(), dma.logger).Debug("msg", "retrieved tokens")
	return ret, nil
}

// handleDeclarationItems handles the retrieval of DM client declaration items.
func (dma *DMAdapter) handleDeclarationItems(r *mdm.Request) ([]byte, error) {
	ret, err := dma.declarationStore.RetrieveDeclarationItemsJSON(r.Context(), r.ID)
	if err != nil {
		return ret, fmt.Errorf("retrieving declaration items: %w", err)
	}

	ctxlog.Logger(r.Context(), dma.logger).Debug("msg", "retrieved declaration items")
	return ret, nil
}

// handleDeclaration handles the declaration retrieval DM endpoint.
func (dma *DMAdapter) handleDeclaration(r *mdm.Request, path string) ([]byte, error) {
	declarationType, declarationID, err := ddm.ParseDeclarationPath(path)
	if err != nil {
		return nil, fmt.Errorf("parsing declaration path: %s: %w", path, err)
	}

	logger := ctxlog.Logger(r.Context(), dma.logger).With(
		logkeys.DeclarationType, declarationType,
		logkeys.DeclarationID, declarationID,
	)

	ret, err := dma.declarationStore.RetrieveEnrollmentDeclarationJSON(r.Context(), declarationID, declarationType, r.ID)
	if err != nil {
		// log the error with the additional context
		logger.Info("msg", "retrieving declaration", "err", err)
		return ret, fmt.Errorf("retrieveing declaration: %s: %w", declarationID, err)
	}

	logger.Debug("msg", "retrieved declaration")
	return ret, nil
}

// DeclarativeManagement adapts DMAdapter to NanoMDM.
// This is the primary DM handler for the four DM protocol endpoints.
func (dma *DMAdapter) DeclarativeManagement(r *mdm.Request, msg *mdm.DeclarativeManagement) ([]byte, error) {
	if r == nil {
		return nil, errors.New("nil request")
	}
	if msg == nil {
		return nil, errors.New("nil message")
	}

	switch msg.Endpoint {
	case "status":
		return nil, dma.handleStatus(r, msg)
	case "tokens":
		return dma.handleTokens(r)
	case "declaration-items":
		return dma.handleDeclarationItems(r)
	}

	const declarationPrefix = "declaration/"
	if strings.HasPrefix(msg.Endpoint, declarationPrefix) {
		return dma.handleDeclaration(r, msg.Endpoint[len(declarationPrefix):])
	}

	return nil, fmt.Errorf("%w: %s", ErrUnknownDMEndpoint, msg.Endpoint)
}
