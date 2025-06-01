// Package cmdservice adapts NanoCMD directly to NanoMDM.
package cmdservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/micromdm/nanocmd/engine"
	cmdmdm "github.com/micromdm/nanocmd/mdm"
	"github.com/micromdm/nanocmd/workflow"
	"github.com/micromdm/nanolib/log"
	"github.com/micromdm/nanolib/log/ctxlog"
	"github.com/micromdm/nanomdm/mdm"
	"github.com/micromdm/nanomdm/service"
	"github.com/micromdm/nanomdm/storage"
	"github.com/micromdm/plist"
)

// MDMEventReceiver receives MDM events. This is a subset of a NanoCMD workflow engine.
type MDMEventReceiver interface {
	MDMCommandResponseEvent(ctx context.Context, id, uuid string, raw []byte, mdmCtx *workflow.MDMContext) error
	MDMCheckinEvent(ctx context.Context, id string, checkin interface{}, mdmCtx *workflow.MDMContext) error

	// MDMIdleEvent is called when an MDM Result Report has an "Idle" status.
	// This is the same endpoint as the command and response but it is handled differently.
	MDMIdleEvent(ctx context.Context, id string, raw []byte, mdmCtx *workflow.MDMContext, eventAt time.Time) error
}

// CMDService is a NanoMDM service that adapts NanoCMD.
type CMDService struct {
	service.CheckinAndCommandService

	logger log.Logger
	engine MDMEventReceiver
	store  storage.TokenUpdateTallyStore

	maskStartedWorkflow bool
}

// Options configure the service.
type Option func(s *CMDService) error

// WithLogger configures logger on a service.
func WithLogger(logger log.Logger) Option {
	if logger == nil {
		panic("nil logger")
	}

	return func(s *CMDService) error {
		s.logger = logger
		return nil
	}
}

// WithMaskAlreadyStarted enables masking of the "workflow already started" error.
// The error is instead logged as message to the service logger, but does not return the error.
// This masking is only for the command-and-report-results endpoint and only for Idle events.
func WithMaskAlreadyStarted() Option {
	return func(s *CMDService) error {
		s.maskStartedWorkflow = true
		return nil
	}
}

// WithTokenUpdateTallyStore configures the NanoMDM token update tally store.
// This allows the service to determine the TokenUpdate count for an
// enrollment and thus whether it is an initial enrollment (or not).
func WithTokenUpdateTallyStore(store storage.TokenUpdateTallyStore) Option {
	if store == nil {
		panic("nil store")
	}

	return func(s *CMDService) error {
		s.store = store
		return nil
	}
}

// New creates a new NanoMDM service that adapts NanoCMD.
func New(engine MDMEventReceiver, opts ...Option) (*CMDService, error) {
	if engine == nil {
		panic("nil engine")
	}

	s := &CMDService{
		CheckinAndCommandService: new(service.NopService),
		logger:                   log.NopLogger,
		engine:                   engine,
	}

	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// checkInFromRaw parses the check-in message from raw into a NanoCMD check-in message.
func checkInFromRaw(messageType string, raw []byte) (any, error) {
	msg := cmdmdm.NewCheckinFromMessageType(messageType)
	if msg == nil {
		return nil, fmt.Errorf("unknown nanocmd message type: %s", messageType)
	}

	if err := plist.Unmarshal(raw, msg); err != nil {
		return nil, fmt.Errorf("unmarshal nanocmd check-in message: %w", err)
	}

	return msg, nil
}

// Authenticate adapts the NanoMDM Authenticate check-in message to a NanoCMD check-in event.
func (s *CMDService) Authenticate(r *mdm.Request, m *mdm.Authenticate) error {
	msg, err := checkInFromRaw(m.MessageType.MessageType, m.Raw)
	if err != nil {
		return fmt.Errorf("parse authenticate check-in message: %w", err)
	}

	err = s.engine.MDMCheckinEvent(r.Context(), r.ID, msg, &workflow.MDMContext{Params: r.Params})
	if err != nil {
		return fmt.Errorf("nanocmd check-in event: %w", err)
	}

	return nil
}

// TokenUpdate adapts the NanoMDM TokenUpdate check-in message to a NanoCMD check-in event.
func (s *CMDService) TokenUpdate(r *mdm.Request, m *mdm.TokenUpdate) error {
	msg, err := checkInFromRaw(m.MessageType.MessageType, m.Raw)
	if err != nil {
		return fmt.Errorf("parse authenticate check-in message: %w", err)
	}

	if tokenMsg, ok := msg.(*cmdmdm.TokenUpdate); ok && s.store != nil {
		// if we have a tally store and this is a NanoCMD TokenUpdate message
		// then try to figure out if this is the first TokenUpdate message
		// i.e. an enrollment
		tally, err := s.store.RetrieveTokenUpdateTally(r.Context(), r.ID)
		if err != nil {
			return fmt.Errorf("retrieving token update tally: %w", err)
		}

		if tally == 1 {
			// first token update means initial enrollment
			// wrap the token update to include the enrollment flag
			tue := &cmdmdm.TokenUpdateEnrolling{
				TokenUpdate: tokenMsg,
				Enrolling:   true,
			}
			if !tue.Valid() {
				return errors.New("invalid token update wrapper")
			}
			// replace the message with our wrapped version.
			// this will signal an initial enrollment to NanoCMD.
			msg = tue
		}
	}

	err = s.engine.MDMCheckinEvent(r.Context(), r.ID, msg, &workflow.MDMContext{Params: r.Params})
	if err != nil {
		return fmt.Errorf("nanocmd check-in event: %w", err)
	}

	return nil
}

// CheckOut adapts the NanoMDM CheckOut check-in message to a NanoCMD check-in event.
func (s *CMDService) CheckOut(r *mdm.Request, m *mdm.CheckOut) error {
	msg, err := checkInFromRaw(m.MessageType.MessageType, m.Raw)
	if err != nil {
		return fmt.Errorf("parse checkout check-in message: %w", err)
	}

	err = s.engine.MDMCheckinEvent(r.Context(), r.ID, msg, &workflow.MDMContext{Params: r.Params})
	if err != nil {
		return fmt.Errorf("nanocmd check-in event: %w", err)
	}

	return nil
}

// CommandAndReportResults adapts the NanoMDM command results to a NanoCMD command response event.
func (s *CMDService) CommandAndReportResults(r *mdm.Request, results *mdm.CommandResults) (*mdm.Command, error) {
	if results.Status == "Idle" {
		err := s.engine.MDMIdleEvent(r.Context(), r.ID, results.Raw, &workflow.MDMContext{Params: r.Params}, time.Now())
		if errors.Is(err, engine.ErrWorkflowAlreadyStarted) && s.maskStartedWorkflow {
			// if the error is that a workflow is already started
			// and we're configured to mask that error then simply
			// log it and continue on.
			ctxlog.Logger(r.Context(), s.logger).Info("msg", err)
		} else if err != nil {
			// otherwise return with the error.
			return nil, fmt.Errorf("nanocmd idle command response event: %w", err)
		}
		return nil, nil
	}

	err := s.engine.MDMCommandResponseEvent(r.Context(), r.ID, results.CommandUUID, results.Raw, &workflow.MDMContext{Params: r.Params})
	if err != nil {
		return nil, fmt.Errorf("nanocmd command response event: %w", err)
	}

	return nil, nil
}
