package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/micromdm/nanohub/nanohub"

	"github.com/alexedwards/flow"
	"github.com/jessepeterson/kmfddm/ddm"
	ddmapi "github.com/jessepeterson/kmfddm/http/api"
	ddmhttp "github.com/jessepeterson/kmfddm/http/ddm"
	"github.com/micromdm/nanocmd/engine"
	cmdenghttp "github.com/micromdm/nanocmd/engine/http"
	"github.com/micromdm/nanolib/envflag"
	nanolibhttp "github.com/micromdm/nanolib/http"
	"github.com/micromdm/nanolib/http/trace"
	"github.com/micromdm/nanolib/log/stdlogfmt"
	nanohttp "github.com/micromdm/nanomdm/http"
	nanoapi "github.com/micromdm/nanomdm/http/api"
	"github.com/micromdm/nanomdm/http/authproxy"
	"github.com/micromdm/nanomdm/mdm"
	"github.com/micromdm/nanomdm/push/nanopush"
	pushservice "github.com/micromdm/nanomdm/push/service"
)

// overridden by -ldflags -X
var version = "unknown"

func getCerts(rootsPath, intsPath string) (rootBytes []byte, intBytes []byte, err error) {
	if rootsPath == "" {
		err = errors.New("no path to CA root")
		return
	}
	rootBytes, err = os.ReadFile(rootsPath)
	if err != nil {
		return
	}
	if intsPath != "" {
		intBytes, err = os.ReadFile(intsPath)
	}
	return
}

func main() {
	var (
		flListen     = flag.String("listen", ":9004", "HTTP listen address")
		flCheckin    = flag.Bool("checkin", false, "enable separate HTTP endpoint for MDM check-ins")
		flVersion    = flag.Bool("version", false, "print version and exit")
		flDebug      = flag.Bool("debug", false, "log debug messages")
		flStorage    = flag.String("storage", "file", "storage backend")
		flDSN        = flag.String("storage-dsn", "", "storage backend data source name")
		flOptions    = flag.String("storage-options", "", "storage backend options")
		flRootsPath  = flag.String("ca", "", "path to PEM CA cert(s)")
		flIntsPath   = flag.String("intermediate", "", "path to PEM intermediate cert(s)")
		flDump       = flag.Bool("dump", false, "dump MDM requests and responses to stdout")
		flCertHeader = flag.String("cert-header", "", "HTTP header containing TLS client certificate")
		flAPIKey     = flag.String("api-key", "", "API key for API endpoints")
		flDMShard    = flag.Bool("dmshard", false, "enable DM shard management properties declaration")
		flWebhookURL = flag.String("webhook-url", "", "URL to send requests to")
		flAuthProxy  = flag.String("auth-proxy-url", "", "Reverse proxy URL target for MDM-authenticated HTTP requests")
		flUAZLChal   = flag.Bool("ua-zl-dc", false, "reply with zero-length DigestChallenge for UserAuthenticate")
		flMigration  = flag.Bool("migration", false, "HTTP endpoint for enrollment migrations")
		flWorkSec    = flag.Uint("worker-interval", uint(engine.DefaultDuration/time.Second), "interval for worker in seconds")
		flPushSec    = flag.Uint("repush-interval", uint(engine.DefaultRePushDuration/time.Second), "interval for repushes in seconds")
		flRetro      = flag.Bool("retro", false, "Allow retroactive certificate-authorization association")
	)

	envflag.Parse("NANOHUB_", []string{"version"})

	if *flVersion {
		fmt.Println(version)
		return
	}

	logger := stdlogfmt.New(stdlogfmt.WithDebugFlag(*flDebug))

	store, dmStore, cmdstore, err := NewStore(*flStorage, *flDSN, *flOptions, logger)
	if err != nil {
		logger.Info("err", err)
		os.Exit(1)
	}

	roots, ints, err := getCerts(*flRootsPath, *flIntsPath)
	if err != nil {
		logger.Info("err", err)
		os.Exit(1)
	}

	pushService := pushservice.New(store, store, nanopush.NewFactory(), logger.With("service", "push"))

	hubOpts := []nanohub.Option{
		nanohub.WithLogger(logger),
		nanohub.WithRootPEMs(roots),
		nanohub.WithIntermediatePEMs(ints),
		nanohub.WithAPNSPush(pushService),
		nanohub.WithUADefault(*flUAZLChal),
	}

	if *flRetro {
		hubOpts = append(hubOpts, nanohub.WithAllowRetroactive())
	}

	if *flCheckin {
		hubOpts = append(hubOpts,
			nanohub.WithCheckinHandler(),
			nanohub.WithoutServerCombinedHandler(),
		)
	}

	if dmStore != nil {
		hubOpts = append(hubOpts,
			nanohub.WithDM(dmStore),
			nanohub.WithDMStatusStore(dmStore, getStatusID),
		)
		if *flDMShard {
			hubOpts = append(hubOpts, nanohub.WithDMShard(nil))
		}
	}

	var subsysStore *subsystemStorage
	if cmdstore != nil {
		hubOpts = append(hubOpts,
			nanohub.WithWF(cmdstore),
			nanohub.WithWFEvents(cmdstore),
		)

		subsysStore, err = SubsystemStorage(*flStorage, *flDSN)
		if err != nil {
			logger.Info("err", err)
			os.Exit(1)
		}

		hubOpts = append(hubOpts, workflows(logger, subsysStore)...)
	}

	if *flCertHeader != "" {
		hubOpts = append(hubOpts, nanohub.WithCertHeader(*flCertHeader))
	} else {
		// default to Mdm-Signature
		hubOpts = append(hubOpts, nanohub.WithMdmSignature())
	}

	if *flDebug {
		hubOpts = append(hubOpts, nanohub.WithMdmSignatureErrorLog())
	}

	if *flDump {
		hubOpts = append(hubOpts, nanohub.WithDumpToStdout())
	}

	if *flWebhookURL != "" {
		hubOpts = append(hubOpts, nanohub.WithWebhook(*flWebhookURL))
	}

	if *flMigration {
		hubOpts = append(hubOpts, nanohub.WithMigration())
	}

	if *flWorkSec > 0 {
		hubOpts = append(hubOpts, nanohub.WithWFWorkerDuration(time.Second*time.Duration(*flWorkSec)))
	}

	if *flWorkSec > 0 {
		hubOpts = append(hubOpts, []nanohub.Option{
			nanohub.WithWFWorker(cmdstore),
			nanohub.WithWFWorkerDuration(time.Second * time.Duration(*flWorkSec)),
		}...)

		if *flPushSec > 0 {
			hubOpts = append(hubOpts, nanohub.WithWFWorkerRePushDuration(time.Second*time.Duration(*flPushSec)))
		}
	}

	nh, err := nanohub.New(store, hubOpts...)
	if err != nil {
		logger.Info("err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()

	mux.Handle("/version", nanolibhttp.NewJSONVersionHandler(version))

	mux.Handle("/mdm", nh.ServerHandler())

	if *flAuthProxy != "" {
		ap, err := nh.NewAuthProxy(
			*flAuthProxy,
			"X-Enrollment-ID",
			authproxy.WithHeaderFunc("X-Trace-ID", trace.GetTraceID),
		)
		if err != nil {
			logger.Info("err", err)
			os.Exit(1)
		}
		if ap == nil {
			logger.Info("err", "nil authproxy handler?")
			os.Exit(1)
		}

		mux.Handle(
			"/authproxy/",
			ap,
		)
	}

	if nh.CheckInHandler() != nil {
		mux.Handle("/checkin", nh.CheckInHandler())
	}

	if *flAPIKey != "" {
		authMW := func(h http.Handler) http.Handler {
			return nanolibhttp.NewSimpleBasicAuthHandler(h, "nanohub", *flAPIKey, "NanoHUB API")
		}

		nanoMux := nanohttp.NewMWMux(http.NewServeMux())
		nanoMux.Use(authMW)
		nanoapi.HandleAPIv1("", nanoMux, logger, store, pushService)
		mux.Handle("/api/v1/nanomdm/",
			http.StripPrefix("/api/v1/nanomdm", nanoMux),
		)

		cmdMux := flow.New()
		cmdMux.Use(authMW)
		// register engine endpoints
		cmdenghttp.HandleAPIv1("", cmdMux, logger, nh.Engine(), cmdstore)
		// register subsystem endpoints
		handleSubsystemAPIs("", cmdMux, logger, subsysStore)
		mux.Handle("/api/v1/nanocmd/",
			http.StripPrefix("/api/v1/nanocmd", cmdMux),
		)

		ddmMux := flow.New()
		ddmMux.Use(authMW)
		ddmapi.HandleAPIv1("", ddmMux, logger, dmStore, nh.DMNotifier())
		ddmMux.Handle(
			"/declaration-items",
			ddmhttp.TokensOrDeclarationItemsHandler(dmStore, false, logger.With("handler", "declaration-items")),
			"GET",
		)
		ddmMux.Handle(
			"/tokens",
			ddmhttp.TokensOrDeclarationItemsHandler(dmStore, true, logger.With("handler", "tokens")),
			"GET",
		)
		ddmMux.Handle(
			"/declaration/:type/:id",
			http.StripPrefix("/declaration/",
				ddmhttp.DeclarationHandler(dmStore, logger.With("handler", "declaration")),
			),
			"GET",
		)
		mux.Handle("/api/v1/ddm/",
			http.StripPrefix("/api/v1/ddm", ddmMux),
		)

		if nh.MigrationHandler() != nil {
			mux.Handle("/migration", authMW(nh.MigrationHandler()))
		}
	}

	if *flWorkSec > 0 {
		nh.GoStartEngineRunner(context.Background())
	}

	var handler http.Handler = mux

	handler = trace.NewTraceLoggingHandler(handler, logger.With("handler", "log"), newTraceID)

	logger.Info("msg", "starting server", "listen", *flListen)
	if err = http.ListenAndServe(*flListen, handler); err != nil {
		logger.Info("msg", "server stopped", "err", err)
		os.Exit(3)
	}
	logger.Debug("msg", "server stopped")
}

// newTraceID generates a new HTTP trace ID for context logging.
// Currently this just makes a random string. This would be better
// served by e.g. https://github.com/oklog/ulid or something like
// https://opentelemetry.io/ someday.
func newTraceID(_ *http.Request) string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func getStatusID(r *mdm.Request, _ *ddm.StatusReport) (string, error) {
	return trace.GetTraceID(r.Context()), nil
}
