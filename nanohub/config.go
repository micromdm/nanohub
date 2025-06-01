package nanohub

import (
	"crypto/x509"
	"errors"
	"os"
	"time"

	"github.com/micromdm/nanohub/cmdservice"
	"github.com/micromdm/nanohub/ddmadapter"

	ddmstorage "github.com/jessepeterson/kmfddm/storage"
	"github.com/jessepeterson/kmfddm/storage/shard"
	"github.com/micromdm/nanocmd/engine"
	cmdstorage "github.com/micromdm/nanocmd/engine/storage"
	"github.com/micromdm/nanocmd/workflow"
	"github.com/micromdm/nanolib/log"
	"github.com/micromdm/nanomdm/certverify"
	"github.com/micromdm/nanomdm/push"
	nanoservice "github.com/micromdm/nanomdm/service"
	"github.com/micromdm/nanomdm/service/certauth"
	"github.com/micromdm/nanomdm/service/dump"
)

// DMStore is the storage required to enable DM.
type DMStore interface {
	ddmstorage.EnrollmentDeclarationDataStorage
	ddmstorage.EnrollmentDeclarationStorage
	ddmstorage.EnrollmentIDRetriever
	ddmstorage.EnrollmentSetRemover
}

// authConfig contains configuration for MDM authentication middleware
type authConfig struct {
	// mdmSignature is true if extracting the authentication certificate
	// from the MDM `Mdm-Signature` header and false if the server will
	// use mTLS authentication.
	mdmSignature bool

	// signatureHeader contains the HTTP header name to use for
	// certificate extraction. If empty authentication will extract
	// the mTLS certificate from the HTTP request (i.e. Go native mTLS).
	signatureHeader string

	// signatureLogErrors enables logging of the `Mdm-Signature` header
	// if MDM signature header extraction is false.
	signatureLogErrors bool
}

// config contains internal configuration options.
type config struct {
	logger     log.Logger
	authConfig authConfig

	migration bool

	checkin    bool // enables the check-in handler
	noCombined bool // disables the "combined" check-in/command handler

	tokenMuxers map[string]nanoservice.GetToken
	dumpWriter  dump.DumpWriter

	certAuthOpts []certauth.Option

	ua        nanoservice.UserAuthenticate
	uaDefault bool
	uazl      bool // UserAuthenticate Zero-Length Challenge mode

	webhookURLs []string

	svcs   []nanoservice.CheckinAndCommandService
	pusher push.Pusher

	verifier  certverify.CertVerifier
	rootsPEM  []byte
	intsPEM   []byte
	keyUsages []x509.ExtKeyUsage

	dmStore   DMStore
	dmDStores []ddmstorage.EnrollmentDeclarationDataStorage
	dmOpts    []ddmadapter.Option
	dmRmSets  bool

	cmdStore       cmdstorage.Storage
	cmdWorkerStore cmdstorage.WorkerStorage
	cmdOpts        []engine.Option
	cmdWorkerOpts  []engine.WorkerOption
	cmdSvcOpts     []cmdservice.Option
	cmdWorkflows   []func(e workflow.StepEnqueuer) (workflow.Workflow, error)
}

// Options configure NanoHUBs.
type Option func(*config) error

// newConfig creates and initializes a new, safe config.
func newConfig() *config {
	return &config{
		logger:      log.NopLogger,
		tokenMuxers: make(map[string]nanoservice.GetToken),
		keyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
}

// validates the internal consistency of c.
func (c *config) validate() error {
	if c.logger == nil {
		return errors.New("nil logger")
	}

	if c.noCombined && !c.checkin {
		return errors.New("config precludes checkin support")
	}

	if c.verifier != nil && (len(c.rootsPEM) > 0 || len(c.intsPEM) > 0) {
		return errors.New("roots and intermediates present with explicit verifier")
	}

	if c.authConfig.signatureHeader != "" && c.authConfig.mdmSignature {
		return errors.New("signature header and Mdm-Signature are mutually exclusive")
	}

	return nil
}

// runOptions configures runs opts on c.
func (c *config) runOptions(opts ...Option) error {
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return err
		}
	}
	return nil
}

type tokenMuxer interface {
	Handle(string, nanoservice.GetToken)
}

func (c *config) attachGetTokenHandlers(muxer tokenMuxer) {
	if muxer == nil {
		panic("nil muxer")
	}

	for serviceType, handler := range c.tokenMuxers {
		muxer.Handle(serviceType, handler)
	}
}

// getOrMakeVerifier returns configured verifier or builds a new pool verifier.
func (c *config) getOrMakeVerifier() (certverify.CertVerifier, error) {
	if c.verifier != nil {
		return c.verifier, nil
	}
	return certverify.NewPoolVerifier(c.rootsPEM, c.intsPEM, c.keyUsages...)
}

// WithLogger is the "root" logger of NanoHUB.
// Other per-service loggers will be spun off from this one.
func WithLogger(logger log.Logger) Option {
	return func(c *config) error {
		c.logger = logger
		return nil
	}
}

// WithCheckinHandler configures the separate check-in HTTP handler.
// Without enabling this check-ins are handled on the single combined handler.
func WithCheckinHandler() Option {
	return func(c *config) error {
		c.checkin = true
		return nil
	}
}

// WithoutServerCombinedHandler disables the combined check-in and command report handler.
// Instead the server handler will only be configured for command reports.
// The separate check-in handler will need to be used.
func WithoutServerCombinedHandler() Option {
	return func(c *config) error {
		c.noCombined = true
		return nil
	}
}

// WithGetTokenForServiceType sets a GetToken handler for serviceType.
func WithGetTokenForServiceType(serviceType string, handler nanoservice.GetToken) Option {
	if serviceType == "" {
		panic("empty service type")
	}
	if handler == nil {
		panic("nil handler")
	}

	return func(c *config) error {
		if _, ok := c.tokenMuxers[serviceType]; ok {
			return errors.New("GetToken service type already registered")
		}

		c.tokenMuxers[serviceType] = handler
		return nil
	}
}

// WithDump dumps the raw MDM responses from enrollments to w.
func WithDump(w dump.DumpWriter) Option {
	return func(c *config) error {
		c.dumpWriter = w
		return nil
	}
}

// WithDump dumps the raw MDM responses from enrollments to stdout.
func WithDumpToStdout() Option {
	return WithDump(os.Stdout)
}

// WithAllowRetroactive turns on the retroactive certificate authorization option.
// This effectively allows migrated devices to "fix" their own authentication.
// Warning: for devices without an existing certificate association this option
// allows devices to potentially spoof another device once.
func WithAllowRetroactive() Option {
	return func(c *config) error {
		c.certAuthOpts = append(c.certAuthOpts, certauth.WithAllowRetroactive())
		return nil
	}
}

// WithVerifier overrides the default certificate "pool" verifier with verifier.
func WithVerifier(verifier certverify.CertVerifier) Option {
	return func(c *config) error {
		c.verifier = verifier
		return nil
	}
}

// WithRootPEMs specifies the PEM bytes of the root CA(s) to verify the
// MDM client identity certificate against using a pool verifier.
func WithRootPEMs(pem []byte) Option {
	return func(c *config) error {
		c.rootsPEM = pem
		return nil
	}
}

// WithIntermediatePEMs specifies the PEM bytes of the intermediate CA(s)
// to verify the MDM client identity certificate against using a pool verifier.
func WithIntermediatePEMs(pem []byte) Option {
	return func(c *config) error {
		c.intsPEM = pem
		return nil
	}
}

// WithMdmSignature enables Mdm-Signature header certificate extraction.
func WithMdmSignature() Option {
	return func(c *config) error {
		c.authConfig.mdmSignature = true
		return nil
	}
}

// WithCertHeader configures the HTTP header name in which the device certificate will be extracted from.
// Either RFC 9440 or a URL encoded PEM certificate formats supported.
// Disables Mdm-Signature header extraction.
func WithCertHeader(header string) Option {
	if header == "" {
		panic("empty header")
	}

	return func(c *config) error {
		c.authConfig.mdmSignature = false
		c.authConfig.signatureHeader = header
		return nil
	}
}

// WithMdmSignatureErrorLog enables raw `Mdm-Signature` header logging when errors occur.
func WithMdmSignatureErrorLog() Option {
	return func(c *config) error {
		c.authConfig.signatureLogErrors = true
		return nil
	}
}

// WithAPNSPush sets the APNs pusher.
// When a service needs to send an APNs push to an enrollment,
// such as when enqueuing a command, pusher is used.
func WithAPNSPush(pusher push.Pusher) Option {
	return func(c *config) (err error) {
		c.pusher = pusher
		return nil
	}

}

// WithWebhook configures a MicroMDM-compatible webhook to callback to url.
func WithWebhook(url string) Option {
	if url == "" {
		panic("empty url")
	}

	return func(c *config) error {
		c.webhookURLs = append(c.webhookURLs, url)
		return nil
	}
}

// WithUA configures the UserAuthenticate service for NanoMDM.
func WithUA(ua nanoservice.UserAuthenticate) Option {
	return func(c *config) error {
		c.ua = ua
		return nil
	}
}

// WithUADefault enables the default NanoMDM UserAuthenticate service.
// Will only be used if no other service was configured via [WithUA].
// The bool uazl enables Zero-Length Digest Challenge response mode.
func WithUADefault(uazl bool) Option {
	return func(c *config) error {
		c.uaDefault = true
		c.uazl = uazl
		return nil
	}
}

// WithMigration enables a NanoMDM "migration" HTTP handler.
func WithMigration() Option {
	return func(c *config) error {
		c.migration = true
		return nil
	}
}

// WithDM enables Declarative Management on the server using store.
func WithDM(store DMStore) Option {
	return func(c *config) error {
		c.dmStore = store
		return nil
	}
}

// WithDMStatusStore enables storing Declarative Management status reports
// using store and status ID generator function fn.
func WithDMStatusStore(store ddmstorage.StatusStorer, fn ddmadapter.StatusIDFn) Option {
	return func(c *config) error {
		c.dmOpts = append(c.dmOpts,
			ddmadapter.WithStatusStore(store),
			ddmadapter.WithStatusIDFn(fn),
		)
		return nil
	}
}

// WithDMSetRemover turns on removal of DM enrollment set associations upon enrollment.
func WithDMSetRemover() Option {
	return func(c *config) error {
		c.dmRmSets = true
		return nil
	}
}

// WithDMShard configures and enables the DM shard storage backend.
// The shard function fn can be nil.
// Should only be used once.
func WithDMShard(fn shard.ShardFunc) Option {
	var shardOpts []shard.Option
	if fn != nil {
		shardOpts = append(shardOpts, shard.WithShardFunc(fn))
	}

	return func(c *config) error {
		c.dmDStores = append(c.dmDStores, shard.NewShardStorage(shardOpts...))
		return nil
	}
}

// WithWF enables the command workflow engine using store.
func WithWF(store cmdstorage.Storage) Option {
	return func(c *config) error {
		c.cmdStore = store
		return nil
	}
}

// WithWFEvents turns on event dispatch using store.
func WithWFEvents(store cmdstorage.EventSubscriptionStorage) Option {
	if store == nil {
		panic("nil workflow event store")
	}

	return func(c *config) error {
		c.cmdOpts = append(c.cmdOpts, engine.WithEventStorage(store))
		return nil
	}
}

// WithWorkflow configures fn to be called and the resulting workflow
// to be registered with the workflow engine.
func WithWorkflow(fn func(e workflow.StepEnqueuer) (workflow.Workflow, error)) Option {
	return func(c *config) error {
		if fn == nil {
			return nil
		}
		c.cmdWorkflows = append(c.cmdWorkflows, fn)
		return nil
	}
}

// WithMaskAlreadyStarted enables masking of the "workflow already started" error.
// The error is instead logged as a message to the service logger, but does not return the error.
// This masking is only for the command-and-report-results endpoint and only for Idle events.
func WithMaskAlreadyStarted() Option {
	return func(c *config) error {
		c.cmdSvcOpts = append(c.cmdSvcOpts, cmdservice.WithMaskAlreadyStarted())
		return nil
	}
}

// WithWFWorker configures the command workflow engine worker using store.
// The worker can be later started from NanoHUB.
func WithWFWorker(store cmdstorage.WorkerStorage) Option {
	return func(c *config) error {
		c.cmdWorkerStore = store
		return nil
	}
}

// WithWFWorkerDuration configures the polling interval for the worker.
func WithWFWorkerDuration(d time.Duration) Option {
	return func(c *config) error {
		c.cmdWorkerOpts = append(c.cmdWorkerOpts, engine.WithWorkerDuration(d))
		return nil
	}
}

// WithWFWorkerRePushDuration configures when enrollments should be sent APNs pushes.
// This is the duration an enrollment ID has not received a response for an MDM command.
func WithWFWorkerRePushDuration(d time.Duration) Option {
	return func(c *config) error {
		c.cmdWorkerOpts = append(c.cmdWorkerOpts, engine.WithWorkerRePushDuration(d))
		return nil
	}
}
