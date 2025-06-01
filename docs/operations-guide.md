# NanoHUB Operations Guide

This is a brief overview of configuring and running the NanoHUB reference server.

NanoHUB adapts and unifies NanoMDM, NanoCMD, and KMFDDM into a single MDM server. You may want to explore those projects' documentation for specific operation.

## Switches

Command line switches for the `nanohub` reference server.

### -api-key string

* API key for API endpoints

API authentication in simply HTTP Basic authentication using "nanohub" as the username and the API key (from this switch) as the password.

### -ca string

* path to PEM CA cert(s)

See the [`-ca` switch of NanoMDM](https://github.com/micromdm/nanomdm/blob/main/docs/operations-guide.md#-ca-string). Operation should be very similar.

### -intermediate string

* path to PEM intermediate cert(s)

See the [`-intermediate` switch of NanoMDM](https://github.com/micromdm/nanomdm/blob/main/docs/operations-guide.md#-intermediate-string). Operation should be very similar.

### -cert-header string

* HTTP header containing TLS client certificate

See the [`-cert-header` switch of NanoMDM](https://github.com/micromdm/nanomdm/blob/main/docs/operations-guide.md#-cert-header-string). Operation should be very similar. If this option is not specified then `Mdm-Signature` header extraction is used (which requires the `SignMessage` MDM enrollment profile key to be set to true.)

### -debug

* log debug messages

Enable additional debug logging.

### -dump

* dump MDM requests and responses to stdout

Dump MDM request bodies (i.e. complete Plists) to standard output for each request.

### -listen string

* HTTP listen address (default ":9004")

Specifies the listen address (interface & port number) for the server to listen on.

### -storage, -storage-dsn, & -storage-options

The `-storage`, `-storage-dsn`, and `-storage-options` flags together configure the storage backend. `-storage` specifies the name of backend type while `-storage-dsn` specifies the backend data source name (e.g. the connection string). The optional `-storage-options` flag specifies options for the backend if it supports them. If no `-storage` backend is specified then `file` is used as a default. 

#### file backend

* `-storage file`

Configure the `file` storage backend. This backend manages MDM, DM, and workflow data within plain filesystem files and directories using a key-value storage system. It has zero dependencies, no options, and should run out of the box. The `-storage-dsn` flag specifies the root filesystem directory for the database. Subdirectories within this root are created for each subsystem. If no `storage-dsn` is specified then `db` is used as a default.

*Example:* `-storage file -storage-dsn /path/to/my/db`

#### mysql backend

* `-storage mysql`

Configures the MySQL storage backend. The `-storage-dsn` flag should be in the [format the SQL driver expects](https://github.com/go-sql-driver/mysql#dsn-data-source-name).
Be sure to create the storage tables with the `schema.sql` file from *each* of the three NanoMDM, NanoCMD, and KMFDDM projects. MySQL 8.0.19 or later is required.

*Example:* `-storage mysql -dsn nanohub:nanohub/mydb`

#### inmem backend

* `-storage inmem`

Configures the `inmem` storage backend. Data is stored entirely in-memory and is completely volatile — the database will disappear the moment the server process exits. The `-storage-dsn` flag is ignored for this storage backend.

> [!CAUTION]
> All data is lost when the server process exits when using the in-memory storage backend.

*Example:* `-storage inmem`

### -dmshard bool

* enable DM shard management properties declaration

Enable an always-on management properties declaration for every enrollment containing a `shard` payload key. See the [upstream docs](https://github.com/jessepeterson/kmfddm/blob/main/docs/operations-guide.md#-shard).

### -webhook-url string

* URL to send requests to

NanoMDM supports a MicroMDM-compatible [webhook callback](https://github.com/micromdm/micromdm/blob/main/docs/user-guide/api-and-webhooks.md) option. This switch turns on the webhook and specifies the target URL.

### -auth-proxy-url string

* Reverse proxy URL target for MDM-authenticated HTTP requests

Enables the authentication proxy and reverse proxies HTTP requests from the server's `/authproxy/` endpoints to this URL if the client provides the device's enrollment authentication. See [docs](https://github.com/micromdm/nanomdm/blob/main/docs/operations-guide.md#authentication-proxy) for more.

### -ua-zl-dc bool

* reply with zero-length DigestChallenge for UserAuthenticate

By default NanoMDM will respond to a `UserAuthenticate` message with an HTTP 410. This effectively declines management of that the user channel for that MDM user. Enabling this option turns on the "zero-length" Digest Challenge mode where NanoMDM replies with an empty Digest Challenge to enable management each time a client enrolls. See [docs](https://github.com/micromdm/nanomdm/blob/main/docs/operations-guide.md#-ua-zl-dc) for more.

### -migration bool

* HTTP endpoint for enrollment migrations

NanoMDM supports a lossy form of MDM enrollment "migration." Essentially if a source MDM server can assemble enough of both Authenticate and TokenUpdate messages for an enrollment you can "migrate" enrollments by sending those Plist requests to the migration endpoint. Importantly this transfers the needed Push topic, token, and push magic to continue to send APNs push notifications to enrollments.

### -worker-interval uint

* interval for worker in seconds

### -repush-interval uint

* interval for repushes in seconds

### -retro bool

* Allow retroactive certificate-authorization association

By default NanoMDM disallows requests which did not have a certificate association setup in their Authenticate message. For new enrollments this is fine. However for enrollments that did not have a full Authenticate message (i.e. for enrollments that were migrated) they will lack such an association and be denied the ability to connect.

> [!WARNING]
> This switch turns on the ability for enrollments with no existing certificate association to create one, bypassing the authorization check and potentially spoofing migrated devices. Note if an enrollment already has an association this will not overwrite it; only if no existing association exists.

### -version

* print version

Print version and exit.

## HTTP endpoints and APIs

### Project APIs

NanoHUB's reference server simply "mounts" each components' API under its own webserver. For example:

* The normal NanoMDM API is available under the `/api/v1/nanomdm/` path.
  * For example to send an APNs push to ID `9876-5432-1012` you would send a request to `http://example.com/api/v1/nanomdm/push/9876-5432-1012` using the NanoHUB API key and normal NanoMDM HTTP API semantics.
* The normal NanoCMD API is avilable under the `/api/v1/nanocmd/` path.
  * For example to start the workflow [io.micromdm.wf.devinfolog.v1](https://github.com/micromdm/nanocmd/blob/main/docs/operations-guide.md#device-information-logger-workflow) on ID `9876-5432-1012` you would send a POST request to `http://example.com/api/v1/nanocmd/workflow/io.micromdm.wf.devinfolog.v1/start?id=9876-5432-1012` using the NanoHUB API key and normal NanoCMD HTTP API semantics.
* The normal KMFDDM API is availabl under the `/api/v1/ddm/` path.
  * For example to retrieve a list of declarations you would send a GET to `http://example.com/api/v1/ddm/declarations` using the NanoHUB API key and normal KMFDDM HTTP API semantics.

Note that some of these projects have helper tools and scripts which may need to be informed of both the new URL and the NanoHUB API username. Check those individual projects tools to see how to change those settings if they support doing that.

### Native endpoints

### MDM

* Endpoint: `/mdm`

The primary MDM endpoint handling both command and report as well as check-in requests by default. If the check-in endpoint is enable this endpoint only handles command and report requests.

### MDM Check-in

* Endpoint: `/checkin`

If enabled with the `-checkin` switch the check-in handler handles MDM check-ins and the primary MDM endpoint `/mdm` only handles command and report requests.

### Migration

* Endpoint: `/migration`

If enabled with the `-migration` switch this will allow MDM check-ins using just the supplied `-api-key` switch for authentication. In this way we effectively support MDM "migration."

### Migration

* Endpoint(s): `/authproxy/`

See the above `-auth-proxy-url` switch. If configured, URLs under this path will be reverse proxied with MDM authentication.

### APIs

* Endpoint(s): `/api/v1/`

See above for explanation of API access.

### Version

* Endpoint: `/version`

Returns a JSON response with the version of the running NanoHUB server.
