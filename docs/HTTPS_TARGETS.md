# Declarative HTTPS targets

## Mapping contract

An administrator-owned target profile with adapter `https/v1` MUST contain an
inline `https` mapping with `api_version: operator.dev/https-mapping/v1alpha1`.
Operator MUST freeze its canonical bytes and digest before campaign execution.
Only the administrator MAY select destinations, authentication, input placement,
response selection, network policy and bounds. The mapping MUST NOT enter
harness mounts, public capability exports or reports. Retained preparation MUST
use a redacted policy and the private mapping's digest.

The mapping MUST contain these fields:

| Field | Contract |
|---|---|
| `origin` | Fixed HTTPS scheme, hostname/IP and optional port; no userinfo, path, query or fragment. |
| `allowed_private_cidrs` | Explicit canonical subnets within RFC1918, IPv6 unique-local, or loopback ranges; at most 32. Omission/empty authorizes no private addresses. |
| `ca_certificates_pem` | Optional administrator-installed CA certificates, embedded to freeze trust bytes; added to ordinary system trust. |
| `authentication` | `mode`: `none`, `bearer`, or `api-key`. The latter two require an existing host `credential_id`; API key additionally requires a fixed `header`. |
| `operations` | 1–64 uniquely identified operations as defined below. |

Each operation MUST define `id`, `method` (`POST`, `PUT`, or `PATCH`),
a fixed absolute clean `path`, `input`, `response`, `maximum_input_bytes`,
`maximum_request_bytes` and `maximum_response_bytes`. Paths MUST reject
query/fragment syntax, escaped paths, traversal and authority overrides.
Input ceilings MUST be 1 byte–1 MiB; encoded request ceilings MUST be at least
the input ceiling and at most 2 MiB; response ceilings MUST be 1 byte–8 MiB.
The profile operation timeout MUST bound the whole request and be at most 30 s.
Unknown fields, duplicate JSON keys and unsupported values MUST fail validation.

`input.format` MUST be `text` or `json`. Text MUST accept UTF-8 `text/plain`
payload bytes directly. JSON MUST accept UTF-8 text as a JSON string or strictly
decoded `application/json` payloads as a JSON value. Optional `input.field`
MUST be an array of object-key strings (at most 16), selecting the destination
within optional `input.fixed` JSON object data. Omitted/empty field selects the
whole body and MUST forbid fixed fields. A nested placement MUST create missing
object parents, reject non-object parents and reject an occupied destination.
Operator MUST serialize data with JSON encoding; it MUST NOT interpolate templates.

`response.format` MUST be `text` or `json`. Text MUST select the bounded UTF-8
response body. JSON MUST strictly decode it and traverse optional `response.field`
object keys. Missing fields/type mismatches MUST produce an explicit mapping
failure. A selected string becomes text; other selected JSON values become JSON
bytes. Unselected response fields and all response headers MUST remain private.

Credential values MUST come from the existing audited resolver. Mappings MUST NOT
contain credential locators or literal authentication values. API-key headers
MUST reject transport, authority, proxy, forwarding, cookie and content-control
headers. Secret values, raw network errors, full URLs and response headers MUST
NOT enter journals, reports or harness inputs.

## Destination and execution contract

Operator MUST validate every resolved address and connect directly to a validated
IP while verifying TLS against the configured hostname (TLS 1.2 or newer).
If any DNS answer is forbidden, execution MUST fail before transmission.
Public unicast addresses are permitted; private/loopback addresses require an
explicit CIDR. Link-local, multicast, unspecified, special-use and known platform
metadata addresses MUST be rejected even if a configured range contains them.

The client MUST ignore ambient proxy settings, reject redirects, retain no cookies,
disable automatic retries and bound headers and response bytes. Every operation
MUST use a fresh connection; the client MUST NOT reuse a connection across attempts.
Additional CA certificates MUST be validated before dispatch. Cancellation and
deadlines MUST cover DNS, connection, TLS, credential resolution and body reads.

The client MUST distinguish failure before any possible request transmission
from uncertain outcomes after transmission might have begun. A response-size
failure or malformed response after dispatch MUST NOT imply the target did not
execute. Only selected bounded feedback and fixed diagnostic codes MAY be retained.

## Campaign integration requirements

Public capabilities MUST describe application operations and `target_output` /
`operation_error` feedback, with empty injection, service, filesystem and snapshot
capabilities. Exports MUST bind a deterministic public companion declaration to
the administrator mapping digest while omitting endpoints and authentication.
Execution MUST rederive this projection from the selected private mapping.

HTTPS campaigns MUST use existing admission/index allocation, artifact verification,
idempotent result replay, observation chunk reads, cancellation and reporting.
Injection setup/cleanup, snapshots, restore and native target stop MUST be unavailable.
Local execution identities MUST NOT be presented as native remote sessions.
Reports MUST state `declared-observer` assurance and MUST NOT imply native oracle
evidence, target reset or remote closure. Recovery MUST stop the harness and
finalize local records without repeating an HTTPS request.

## Validation

Tests MUST cover mapping validation, exact input placement, response selection,
cross-target rejection, forbidden DNS results, rebinding resistance, TLS and custom
CA validation, proxy/redirect rejection, response ceilings, audited credentials,
timeouts, cancellation, uncertain effects, duplicate suppression, feedback narrowing
and retained report assurance. Real target and native host qualification remain
separate from local TLS-fixture tests.
