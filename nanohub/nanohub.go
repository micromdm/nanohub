// Package nanohub is an Apple MDM server.
package nanohub

import (
	"context"
	"errors"
	"fmt"
	"hash"
	"net/http"

	"github.com/micromdm/nanohub/cmdservice"
	"github.com/micromdm/nanohub/ddmadapter"
	"github.com/micromdm/nanohub/enqueue"
	"github.com/micromdm/nanolib/log"

	"github.com/cespare/xxhash"
	"github.com/jessepeterson/kmfddm/notifier"
	ddmstorage "github.com/jessepeterson/kmfddm/storage"
	"github.com/micromdm/nanocmd/engine"
	"github.com/micromdm/nanocmd/logkeys"
	"github.com/micromdm/nanocmd/workflow"
	nanoapi "github.com/micromdm/nanomdm/api"
	"github.com/micromdm/nanomdm/cryptoutil"
	"github.com/micromdm/nanomdm/http/authproxy"
	nanohttpmdm "github.com/micromdm/nanomdm/http/mdm"
	nanoservice "github.com/micromdm/nanomdm/service"
	"github.com/micromdm/nanomdm/service/certauth"
	"github.com/micromdm/nanomdm/service/dump"
	"github.com/micromdm/nanomdm/service/multi"
	"github.com/micromdm/nanomdm/service/nanomdm"
	"github.com/micromdm/nanomdm/service/webhook"
	nanostorage "github.com/micromdm/nanomdm/storage"
)

type DMNotifier interface {
	// Change notifies enrollments when changes to DM happen.
	// Notification entails enqueuing the DM command and pushing to enrollments.
	Changed(ctx context.Context, declarations []string, sets []string, ids []string) error
}

// Engine is a subset of a command workflow engine.
type Engine interface {
	// WorkflowRegistered returns true if the workflow name is registered.
	WorkflowRegistered(name string) bool

	// StartWorkflow starts a new workflow instance for workflow name.
	StartWorkflow(ctx context.Context, name string, context []byte, ids []string, e *workflow.Event, mdmCtx *workflow.MDMContext) (string, error)
}

type runner interface {
	// Run runs the command workflow engine runner.
	Run(ctx context.Context) error
}

// NanoHUB is an MDM server.
type NanoHUB struct {
	logger     log.Logger
	nanomdm    http.Handler
	checkin    http.Handler
	migration  http.Handler
	engine     Engine
	dmNotifier DMNotifier
	authMW     func(http.Handler) http.Handler
	car        nanostorage.CertAuthRetriever
	runner     runner
}

type Store interface {
	nanostorage.ServiceStore
	nanostorage.CertAuthStore
	nanostorage.TokenUpdateTallyStore
	nanostorage.CommandEnqueuer
	nanostorage.PushStore
	nanostorage.PushCertStore
	nanostorage.CertAuthRetriever
}

// New creates a new NanoHUB MDM server.
func New(store Store, opts ...Option) (*NanoHUB, error) {
	if store == nil {
		panic("nil store")
	}

	config := newConfig()
	if err := config.runOptions(opts...); err != nil {
		return nil, err
	}

	if err := config.validate(); err != nil {
		return nil, err
	}

	// the "core" NanoMDM service options
	nanoOpts := []nanomdm.Option{
		nanomdm.WithLogger(config.logger.With("service", "nanomdm")),
	}

	// optionally configure UserAuthenticate check-in handling
	if config.ua != nil {
		nanoOpts = append(nanoOpts, nanomdm.WithUserAuthenticate(config.ua))
	} else if config.uaDefault {
		nanoOpts = append(nanoOpts, nanomdm.WithUserAuthenticate(nanomdm.NewUAService(store, config.uazl)))
	}

	if len(config.tokenMuxers) > 0 {
		// make a new muxer for GetToken support
		tokenMux := nanomdm.NewTokenMux()

		// attach any optioned GetToken handlers to our token muxer
		config.attachGetTokenHandlers(tokenMux)

		// add the muxer to the service
		nanoOpts = append(nanoOpts, nanomdm.WithGetToken(tokenMux))
	}

	// create the NanoHUB!
	hub := &NanoHUB{logger: config.logger, car: store}

	// create NanoMDM API result enqueuer
	nanoPushEnq, err := nanoapi.NewPushEnqueuer(store, config.pusher, nanoapi.WithLogger(config.logger.With("service", "enqueue")))
	if err != nil {
		return nil, fmt.Errorf("creating push enqueuer: %w", err)
	}

	// create NanoHUB enqueue wrapper around NanoMDM API result enqueuer.
	// satisfies both DM and NanoCMD command enqueuer interfaces.
	pushEnq := enqueue.New(nanoPushEnq)

	svcs := config.svcs

	// declarative management configuration
	if config.dmStore != nil {
		var dmStore ddmstorage.EnrollmentDeclarationStorage = config.dmStore
		if len(config.dmDStores) >= 1 {
			// if we have additional DM declaration storages configured
			// then wrap them in a Multi storage wrapped by a JSONAdapt.
			dmStore = ddmstorage.NewJSONAdapt(
				ddmstorage.NewMulti(
					append(config.dmDStores, config.dmStore)...,
				),
				func() hash.Hash { return xxhash.New() },
			)
		}

		dmAdapter, err := ddmadapter.New(dmStore, append(config.dmOpts,
			ddmadapter.WithLogger(config.logger.With("service", "dm")),
		)...)
		if err != nil {
			return nil, fmt.Errorf("creating DM adapter: %w", err)
		}

		nanoOpts = append(nanoOpts, nanomdm.WithDeclarativeManagement(dmAdapter))

		hub.dmNotifier, err = notifier.New(pushEnq, config.dmStore, notifier.WithLogger(config.logger.With("service", "notifier")))
		if err != nil {
			return nil, fmt.Errorf("creating notifier: %w", err)
		}

		if config.dmRmSets {
			svcs = append(svcs, ddmadapter.NewSetsRemover(config.dmStore, nil))
		}
	}

	// create 'core' MDM service
	var nanoSvc nanoservice.CheckinAndCommandService = nanomdm.New(store, nanoOpts...)

	// command workflow (NanoCMD) configuration
	if config.cmdStore != nil {
		e := engine.New(
			config.cmdStore,
			pushEnq,
			append(
				[]engine.Option{engine.WithLogger(config.logger.With("service", "nanocmd"))},
				config.cmdOpts...,
			)...,
		)

		hub.engine = e

		// create the adapter
		cmdSvc, err := cmdservice.New(e, append(config.cmdSvcOpts,
			cmdservice.WithTokenUpdateTallyStore(store),
			cmdservice.WithLogger(config.logger.With("service", "cmdservice")),
		)...)
		if err != nil {
			return nil, fmt.Errorf("creating nanocmd service: %w", err)
		}

		// add our adapter service to list of services
		svcs = append([]nanoservice.CheckinAndCommandService{cmdSvc}, svcs...)

		// create and register any workflows
		for _, fn := range config.cmdWorkflows {
			if fn == nil {
				continue
			}
			w, err := fn(e)
			if err != nil {
				return nil, fmt.Errorf("creating workflow: %w", err)
			}
			if err = e.RegisterWorkflow(w); err != nil {
				return nil, fmt.Errorf("registering workflow: %w", err)
			}
		}

		if config.cmdWorkerStore != nil {
			// configure command workflow engine worker
			hub.runner = engine.NewWorker(
				e,
				config.cmdWorkerStore,
				pushEnq,
				append(config.cmdWorkerOpts, engine.WithWorkerLogger(config.logger.With("service", "worker")))...,
			)
		}
	}

	if len(config.webhookURLs) >= 1 {
		// configure any webhooks
		for _, url := range config.webhookURLs {
			svcs = append(svcs, webhook.New(url, webhook.WithTokenUpdateTalley(store)))
		}
	}

	if len(svcs) >= 1 {
		// wrap all of the supplementary NanoMDM services in a mutli-service adapter.
		nanoSvc = multi.New(
			config.logger.With("service", "multi"),
			// make sure the core NanoMDM service is first
			append([]nanoservice.CheckinAndCommandService{nanoSvc}, svcs...)...,
		)
	}

	// wrap the core service in certificate authorization middleware
	nanoSvc = certauth.New(
		nanoSvc,
		store,
		append(config.certAuthOpts, certauth.WithLogger(config.logger.With("service", "certauth")))...,
	)

	if config.dumpWriter != nil {
		// wrap the service in the dumper middleware
		nanoSvc = dump.New(nanoSvc, config.dumpWriter)
	}

	verifier, err := config.getOrMakeVerifier()
	if err != nil {
		return nil, err
	}

	// wrapped in "double" function to avoid keeping a reference to the config struct
	hub.authMW = func(ac authConfig, cvl, cel log.Logger) func(h http.Handler) http.Handler {
		return func(h http.Handler) http.Handler {
			// as the last wrapped step before the service, verify the cert validity
			h = nanohttpmdm.CertVerifyMiddleware(h, verifier, cvl)

			if ac.mdmSignature {
				// Mdm-Signature header is configured
				return nanohttpmdm.CertExtractMdmSignatureMiddleware(
					h,
					nanohttpmdm.MdmSignatureVerifierFunc(cryptoutil.VerifyMdmSignature),
					nanohttpmdm.SigLogWithLogger(cel),
					nanohttpmdm.SigLogWithLogErrors(ac.signatureLogErrors),
				)
			}

			// mTLS is (default) configured
			if ac.signatureHeader != "" {
				// signature header name present, extract from header
				return nanohttpmdm.CertExtractPEMHeaderMiddleware(h, ac.signatureHeader, cel)
			}

			// default to mTLS (i.e. Go native mTLS) extraction
			return nanohttpmdm.CertExtractTLSMiddleware(h, cel)
		}
	}(
		config.authConfig,
		config.logger.With("handler", "cert-verify"),
		config.logger.With("handler", "cert-extract"),
	)

	// create the primary "ServerURL" handler
	if config.noCombined {
		hub.nanomdm = nanohttpmdm.CommandAndReportResultsHandler(nanoSvc, config.logger.With(
			"service", "handler",
			"handler", "server",
		))
	} else {
		hub.nanomdm = nanohttpmdm.CheckinAndCommandHandler(nanoSvc, config.logger.With(
			"service", "handler",
			"handler", "server",
		))
	}
	hub.nanomdm = hub.authMW(hub.nanomdm)

	if config.checkin {
		// create the separate "CheckInURL" handler
		hub.checkin = nanohttpmdm.CheckinHandler(nanoSvc, config.logger.With(
			"service", "handler",
			"handler", "checkin",
		))
		hub.checkin = hub.authMW(hub.checkin)
	}

	if config.migration {
		// create the migration handler
		hub.migration = nanohttpmdm.CheckinHandler(nanoSvc, config.logger.With(
			"service", "handler",
			"handler", "migration",
		))
	}

	return hub, nil
}

// ServerHandler returns the primary "ServerURL" HTTP handler.
func (nh *NanoHUB) ServerHandler() http.Handler {
	return nh.nanomdm
}

// CheckInHandler returns the separate "CheckInURL" HTTP handler
// if it was configured or nil.
func (nh *NanoHUB) CheckInHandler() http.Handler {
	return nh.checkin
}

// MigrationHandler returns an HTTP migration handler if one was configured or nil.
// Note that this handler is "trusted" and not authenticated.
// It will blindly allow for overwriting existing enrollment data.
// It should be wrapped in appropriate API authentication.
func (nh *NanoHUB) MigrationHandler() http.Handler {
	return nh.migration
}

// Engine returns an interface that runs against the command workflow engine.
// May be nil if the command workflow engine was not configured.
func (nh *NanoHUB) Engine() Engine {
	return nh.engine
}

// DMNotifier returns the DMNotifier.
// Ostensibly to support API endpoints.
func (nh *NanoHUB) DMNotifier() DMNotifier {
	return nh.dmNotifier
}

// GoStartEngineRunner spawns the command workflow engine runner in the background.
func (nh *NanoHUB) GoStartEngineRunner(ctx context.Context) {
	if nh.runner == nil {
		return
	}
	go func(runner runner, logger log.Logger) {
		err := runner.Run(ctx)
		logs := []interface{}{logkeys.Message, "engine worker stopped"}
		if err != nil {
			logger.Info(append(logs, logkeys.Error, err)...)
			return
		}
		logger.Debug(logs...)
	}(nh.runner, nh.logger)
}

// IDAuthMiddleware wraps h in the same MDM authentication-requiring
// HTTP handlers that the MDM protocol uses.
// This is ostensibly to support Declarative Managament asset URLs that
// have MDM specified as their authentication.
// Returns nil if the storage, authentication middleware,
// or logging is not configured.
func (nh *NanoHUB) IDAuthMiddleware(h http.Handler) http.Handler {
	if nh.authMW == nil || nh.car == nil || nh.logger == nil {
		return nil
	}
	// first, wrap h in the cert enrollment ID lookup middleware
	h = nanohttpmdm.CertWithEnrollmentIDMiddleware(h, certauth.HashCert, nh.car, true, nh.logger.With("handler", "with-enrollment-id"))
	// then, proceed to wrap it in our configured MDM authentication
	return nh.authMW(h)
}

// NewAuthProxy creates a new NanoMDM "authproxy" handler.
// It is wrapped in MDM authentication (see [IDAuthMiddleware]).
// It should provide the enrollment ID to the proxied URL in idHeaderName.
// Note you may wish to add any WithHeaderFunc() options for additional
// headers (i.e. trace IDs, etc.) to identify the request downstream.
func (nh *NanoHUB) NewAuthProxy(dest string, idHeaderName string, opts ...authproxy.Option) (http.Handler, error) {
	if dest == "" {
		return nil, errors.New("empty destination URL")
	}
	if idHeaderName == "" {
		return nil, errors.New("empty ID header name")
	}

	authProxy, err := authproxy.New(dest,
		authproxy.WithLogger(nh.logger.With("handler", "authproxy")),
		// populate a header with the discovered enrollment ID
		authproxy.WithHeaderFunc(idHeaderName, nanohttpmdm.GetEnrollmentID),
	)
	if err != nil {
		return nil, err
	}

	return nh.IDAuthMiddleware(authProxy), nil
}
