# Native Interceptor client

Status: the initial host-only client is implemented in `internal/interceptor`.
It provides campaign attachment, instance status, immutable v1alpha2 operation
encoding/dispatch and typed closure confirmation. It does not start a campaign or
serve as the harness-facing policy broker. Shared Operator/Attack Harness schemas
and Interceptor's existing API are unchanged.

The authoritative native contracts remain Interceptor's
[local integration guide](../../interceptor_sandbox/docs/local-api.md) and
[operation wire guide](../../interceptor_sandbox/docs/cross-vm-gateway.md).

## Transport

`interceptor.New()` connects only to `http://127.0.0.1:8080`, using a literal IPv4
loopback dial. There is no configurable URL, DNS lookup, proxy, authentication or
TLS setting. Methods select fixed routes. Campaigns and guest requests cannot
provide destinations, headers or credentials.

Requests use POST and JSON, with `Accept-Encoding: identity`. Redirects and encoded
responses are rejected. Response headers are bounded to 16 KiB. This initial client
bounds both complete JSON request and response envelopes to **5 MiB**, checking
actual response reads even when Content-Length is absent or incorrect. Larger
artifact envelopes are not supported by this stage; future artifact admission must
account for base64/envelope overhead and this client ceiling before dispatch.
Evidence archive streaming is a separate transport still to be implemented.

Attachment/status have a 30-second total deadline. Native dispatch uses the earlier
of the saved request deadline, caller context deadline and a 300-second transport
ceiling. Dialing is bounded to five seconds. The broker must select shorter
operation-specific deadlines and remaining campaign time before saving the request.
No method renews a saved native deadline. Expired/canceled requests are refused
before dispatch; reconciliation uses a fresh `operation.status` request.

The client disables connection reuse and never supplies replayable HTTP bodies or
idempotency headers. It makes no automatic retries. A closure/status request does
not share a client-side mutation lock or connection queue with an in-flight native
operation. Serialization/admission belongs to the host broker and native supervisor.

## Exact native requests and responses

`PrepareOperation(request, body)` creates an immutable `PreparedOperation` with
`Bytes()` and `Request()` accessors. The host must journal those exact bytes and
request identity before dispatching an effect through `Client.Execute`.

Preparation fills the native API version and body digest when omitted, validates
IDs, operation names, deadline and attribution, and enforces the byte bound.
It preserves interior whitespace, number spellings and HTML-sensitive characters
in the body. The digest is SHA-256 of the exact embedded body bytes, **not** the
shared contract's canonical object digest. Outer whitespace is rejected because
native JSON value decoding does not preserve it. Returned byte slices are copies.
Native uint64 revisions retain their native range; they are not decoded through
the shared `jcs-v1` safe-number profile.

Native JSON framing rejects duplicate keys at every nesting level, invalid UTF-8,
non-object roots, trailing values and depth above 64. Transport/typed wrapper
decoders require exact field spellings and required fields, including zero-valued
`session_revision` and false-valued status flags. Responses must match their HTTP
status; native 204 uses HTTP 200 and may contain `body: null` or omit the body.
A successful transport response can still be native 202, 4xx or 5xx.

`Execute` returns that native response without treating transport success as
execution success. `Response.Code()` extracts a bounded symbolic code for host
handling. A native `operation_in_progress`, outcome-unknown code or missing record
must be reconciled under the campaign's terminal/unknown-outcome rules; none permits
reissuing an uncertain effect under a new operation ID.

Transport loss, redirects, malformed/oversized replies and deadlines during a sent
request return `CallError{Uncertain: true}`. Local validation or cancellation before
dispatch cannot send the request. Error strings do not include server bodies,
credentials or host paths. Typed attachment/status/closure helpers return a
`RemoteError` for valid non-200 native responses, retaining the response for host
interpretation. The caller must not infer non-execution solely from an HTTP status.

## Campaign attachment and status

`Client.Attach(ctx, campaignID, workerID, allowTargetStop)` posts the actual host
worker attribution and explicit final-stop choice. It checks returned campaign,
session and worker bindings, v1alpha2 compatibility, running native phase,
feedback profile and evidence ceiling. It checks agreement among the native
session and capability manifest's declared environment/application/capability
identities. Native session revision and campaign run revision are separate values.

The returned `Attachment` contains a small routing projection plus the original
native metadata/capability JSON for host validation and audit. These are protected
host data: session metadata can include host paths. They are never a guest response.
Full native capability digest verification, native-to-public capability projection,
feedback narrowing and target admission remain required adapter work; matching
declared digests alone does not establish those properties.

`Client.Status(ctx, campaignID)` checks the integrated instance ID, active binding,
retained session membership, phase, closure/store flags and optional terminal
failure. `Status.Ready()` requires ready phase, open admission, an available store
and no recorded failure. It is a status observation, not permission to dispatch.
Malformed or unavailable status is not healthy status.

The broker must persist the initially accepted instance/session binding and check
subsequent results against it. `Status.Matches(instanceID, binding)` compares the
instance, session and run revision while ignoring historical worker attribution.
It does not adopt replacements or change the host binding. A new Interceptor
instance after service loss cannot resume the old campaign. Explicit successful
restore is the only normal path for adopting a replacement session/revision.

## Terminal admission closure

`PrepareClose(request)` constructs only `session.owner` with `close_execution` and
the addressed campaign/session. Persist it, dispatch it through `Execute`, then
use `DecodeClosure(response, campaignID)`. Public bind is rejected; binding uses
attachment. Native `session.stop` is excluded from this operation path because
confirmed target stop belongs to the separate lifecycle route.

Closure confirmation requires the direct native owner object, matching campaign,
valid original attribution and a non-null closure timestamp. Live and retained
stopped/error sessions use that same shape. The original worker/run revision are
recorded attribution and need not match the closing caller. A wrapped `{owner: ...}`
or incomplete acknowledgement is rejected. Admission closure does not establish
target termination; it also cannot be used as the prelude to a healthy restore.

## Tests and remaining integration

Tests use copied native fixtures with [recorded provenance](../internal/interceptor/testdata/README.md),
controlled responses and a real loopback HTTP test server. They cover exact native
body encoding, byte freezing, request/response bounds, malformed responses,
redirect/compression rejection, expiry, native 204 framing, binding mismatches,
terminal status, original closure attribution and lost replies without automatic
replay. The loopback server is a protocol test double, not a running Interceptor
target or a qualification result.

Lifecycle restore/stop and operation-record decoding, snapshot inventory, protected
evidence streaming, typed experiment bodies/results, capability projection and
feedback filtering remain adapter work. Durable dispatch/reconciliation, status
polling, terminal fencing and the campaign CLI still need integration. No command
in this stage attaches to or mutates a real local target automatically.
