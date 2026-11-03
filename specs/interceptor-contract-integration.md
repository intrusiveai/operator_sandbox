# Interceptor contract compatibility

## Outcome and scope

Operator MUST automatically verify that its Interceptor integration supports the
contract exposed by the running Interceptor before attaching a campaign. Matching
versions permit the existing preparation and execution workflow to proceed. A
failed check MUST explain the compatibility problem and prevent attachment and
target execution.

Administrators MUST NOT need to export a contract, configure an expected version,
or compare product release numbers. Operator MUST use the existing local HTTP API,
preparation diagnostics, retained records, and release pipeline. No additional
service, compatibility negotiation, or Internet lookup is required.

This implementation is confined to Operator. Interceptor owns its contract and
already implements the discovery endpoint. Attack Harness does not communicate
with Interceptor directly and needs no new tool or startup field for this check.

## Sources and existing behavior

Implementers MUST read these sources together:

- Interceptor: `../interceptor_sandbox/specs/interceptor-operator-contract.md`,
  `internal/integrationcontract/contract.json` and `contract.go`,
  `internal/localapi/service.go` and `contract_test.go`, and `docs/local-api.md`.
- Interceptor's `examples/local-api-smoke/contract.go` and `contract_test.go`
  demonstrate its consumer agreement. Operator MUST reuse its own bounded,
  strict HTTP client rather than copying the example's general HTTP client.
- Operator: [Interceptor client](../docs/INTERCEPTOR_CLIENT.md),
  [campaign startup](../docs/CAMPAIGN_START.md), `internal/interceptor`,
  `internal/hostrun/installed.go`, `attachment.go`, and `diagnostic.go`.
- Operator release code: `internal/hostrelease`, `cmd/operatorctl/release.go`,
  `scripts/build_host_release.py`, `scripts/publish_host_release.py`,
  `.github/workflows/verify-release.yml`, and `tools/release-publisher`.
- The current releases skill and its product-integration and publisher references.
  Publisher updates MUST follow the complete reviewed-export procedure there.

The reviewed Interceptor implementation at source revision
`684c8468f9c668951a8a7f717e71bf12edf8ef4a` embeds contract `0.1.0`. Its endpoint
returns before host reconciliation, attachment, or target access. The contract
describes implemented product capabilities; the existing environment manifest
still describes the capabilities available in a particular sandbox.

Operator currently attaches through `hostrun.attachRecorded` without this check.
It also performs prior-campaign native recovery earlier in installed preparation.
Its release manifest and catalog generator carry only the Operator–Attack Harness
contract identity. The installed publisher snapshot does not recognize
`interceptor_contract_version`. Implementation MUST address all three paths without
mistaking this new contract for the existing environment manifest or Harness
contract.

## 1. One authoritative Operator integration pin

Operator MUST define one release-independent supported Interceptor contract
version, initially `0.1.0`, alongside its native integration code. A small
`internal/interceptor/contract.go` is the preferred location. Runtime comparison,
CLI version reporting, and release metadata MUST derive from that same source.

The pin MUST NOT be an administrator override, environment variable, version
range, list of historical contracts, or value discovered from an installed
Interceptor. Changing it requires review of the integration and its fixtures.
Operator MUST NOT copy Interceptor's full capability contract into Harness inputs
or make it a second environment capability manifest.

Three identities MUST remain distinct:

| Identity | Purpose |
| --- | --- |
| Operator product version | Identifies the Operator release |
| `contract_package_version` | Identifies the Operator–Attack Harness package |
| `interceptor_contract_version` | Identifies the Interceptor contract implemented by Operator |

The new version MUST be an exact `major.minor.patch` string with no `v` prefix,
ranges, suffixes, whitespace, or leading zeroes, and at most 128 characters.
This reuses the catalog's parsing constraint. Product release versions MUST NOT
be compared to establish integration compatibility.

This host-only check MUST NOT introduce a new Harness compatibility mechanism.
Changes to files covered by the existing shared-package identity MUST follow its
normal regeneration and pinning rules; a separate contract-version bump MUST NOT
be invented merely because the Interceptor relationship now has its own pin.

## 2. Typed discovery through the existing client

Add a typed version query and exact-match check in `internal/interceptor`, using
the same client instance and fixed local destination used for subsequent native
requests:

```http
POST /v1/contract-version
Content-Type: application/json

{}
```

The implemented successful response is:

```json
{
  "status": 200,
  "body": {"interceptor_contract_version": "0.1.0"},
  "session_revision": 0
}
```

The query MUST require neither a campaign/session ID nor prior knowledge of the
server's version. Operator MUST validate the HTTP status, existing response
envelope, JSON object and version type/format before comparing exact strings.
The discovery response's session revision MUST NOT become a campaign binding or
be used as an expected revision for later operations.

Reuse the existing query timeout, caller cancellation, response-size bounds,
strict JSON decoding, local destination restriction, and disabled proxies,
redirects and automatic replay. The new query MUST NOT reset a shorter caller
deadline or introduce another configurable network destination. Any smaller
discovery response bound MUST reuse an existing suitable bound and document it.

The check MUST reject a mismatch, missing or malformed version, malformed or
unreadable response, unavailable endpoint, transport failure, cancellation, or
timeout. It MUST NOT fall back to a CLI invocation, full-contract export,
capability-list comparison, release catalog, or an older endpoint.

A failed read-only discovery request MUST NOT be described as an uncertain target
mutation. The generic client's `CallError.Uncertain` currently also covers lost
read responses; the compatibility diagnostic MUST explain what was actually
attempted without changing uncertainty handling for attachment or native writes.

## 3. Campaign admission and lifecycle placement

For `interceptor/v1`, every new campaign MUST perform a fresh successful check
before creating its attachment intent or issuing `/v1/attach`. Place the gate in
the existing recorded-attachment path, or an equally explicit common caller that
cannot bypass it. Preserve the sequence after compatibility succeeds:

1. Retain attachment intent.
2. Attach using the current campaign and worker identities.
3. Retain the returned binding and verify instance/status identity.
4. Validate the environment manifest, submitted capabilities, target profile,
   feedback policy and remaining campaign requirements.
5. Start normal execution through the existing ownership and admission paths.

A matching version MUST NOT grant permissions, enable unavailable capabilities,
relax manifest/digest validation, or imply that the target is healthy. An
incompatible server MUST receive no new-campaign attachment, invocation, injection,
snapshot, restore, or target-stop request as a consequence of the failed start.

The check MUST run in the actual worker preparation path so foreground and optional
service-manager execution behave identically. Offline submission/validation,
capability-file conversion, and an HTTPS-only campaign with no pending native
work MUST NOT acquire a dependency on a running Interceptor.

Audit the earlier `startup.Gate.FinalizeNative` path and explicit late-evidence
collection. Before a fresh recovery or collection client contacts Interceptor
for an existing campaign, it MUST use the same version check. Perform it only if
native work is needed, before claiming any one-shot native recovery dispatch.
An incompatible/unavailable server MUST leave native cleanup or collection
unconfirmed with an actionable reason; it MUST NOT consume a recovery attempt
as if native work had been dispatched. Reuse existing recovery outcomes and
pending records rather than adding a new recovery system.

Compatibility discovery MUST NOT block independently owned Docker termination,
local spool cleanup, or inspection/reporting from retained local evidence.
Previously retained successful checks MUST NOT authorize a fresh client after
process loss or replacement. Normal operations within an established campaign
reuse the check and existing instance/binding validation; do not add a version
request before every native operation or restart Harness after a snapshot restore.

The discovery endpoint does not return an instance token and is not an atomic
attachment handshake. Operator MUST preserve the current attachment and instance
checks and MUST NOT claim that a version response alone proves process continuity.

## 4. Actionable, durable diagnostics

Use the existing `PreparationFailure`/`PreparationDiagnostic` path with stage
`Interceptor compatibility check` and the existing `preparation_failed`
classification. Add a small typed compatibility error or bounded optional
diagnostic detail where needed; do not introduce a separate error framework.

Diagnostics MUST distinguish at least version mismatch, unavailable endpoint,
invalid/missing version, invalid response, transport failure, timeout, and
cancellation. For valid mismatches, retain the expected and actual contract
versions. Preserve HTTP status when observed. Missing or invalid values MUST be
identified as such rather than echoed unvalidated or replaced with a guessed
version. Existing primary-error and secondary-cleanup handling MUST remain intact.

Illustrative foreground output:

```text
Campaign preparation failed during Interceptor compatibility check (preparation_failed):
Operator requires Interceptor contract 0.1.0; the running Interceptor reports 0.2.0.
Use an Interceptor release implementing contract 0.1.0, or an Operator release compatible with 0.2.0.
Execution will not resume.
```

The same cause MUST be available through a separate inspection invocation using
the failed start record, including failures before a campaign journal exists.
Supervised execution MUST retain equivalent details. Preserve compatibility with
older start/completion records that lack these optional diagnostics.

Successful campaigns SHOULD retain the checked expected/actual version and time
in existing campaign preparation evidence once a journal exists. Reuse explicit
metadata projections if this fact is exported. Do not create a separate discovery
database or retain unrestricted response bodies, headers, private URLs or secrets.

## 5. Release identity and publication

Extend `operatorctl version`'s existing JSON output with
`interceptor_contract_version`, from the authoritative integration pin. Preserve
the existing Harness contract fields and source/product identity. The command
MUST work offline without Docker, Interceptor, a model provider or cloud access.

Carry the pin through `hostrelease.Manifest`, manifest generation/checking,
`build_host_release.py`, build provenance, candidate inventory, and
`publish_host_release.py`. New candidate output MUST require a valid pin and
agreement across all four host platforms. Catalog entries MUST contain both
`contract_package_version` and `interceptor_contract_version`.

Release verification MUST compare the reported pin from each actual packaged
`operatorctl` executable, on a compatible runner, with its archive metadata and
candidate inventory. Retain the result with the archive checksum and source
revision. Extend the existing build/install checks; do not duplicate the full
product suite per platform or require Interceptor's release workflow/test suite.
The current native installed-package test runs only for Linux AMD64, so source
inspection or that one test alone does not verify every archived executable.

Existing strict host-manifest decoders and installer paths MUST be updated
together. Preserve inspection of historical metadata where currently supported;
absence in an old record means unknown, not `0.1.0`. New candidate publication
MUST reject missing pins. Do not rewrite existing immutable releases or infer
missing historical contract versions from installed software.

Upgrade `tools/release-publisher` using a complete reviewed export supporting
both contract fields, including `publisher-source.json`, all tests and deletions.
The reviewed skill snapshot identifies releases source
`381271ccaa48a1af631ea052217b97196735786d` or a reviewed successor as the source
for publication code. Obtain an Operator-specific export; do not copy Interceptor's
product provenance or hand-edit individual vendored files. Publication MUST NOT
fetch private publisher code at runtime.

Candidate replacement and promotion MUST preserve the existing artifact-integrity
rules and both contract identities. A rebuilt candidate may change its pin only
with changed artifacts and repeated verification. Promotion MUST preserve the
qualified bytes and both versions. Existing historical catalog entries MUST remain
intact; no missing contract field may be fabricated for them.

Keep the existing workflow triggers, protected release tags, environment approval,
catalog role, conditional writes and exact cache invalidation. Validate entries
before acquiring cloud credentials. Integrate workflow changes through the normal
reviewed release path and make publication code available at the new publishing
tag; do not move old immutable tags. This task authorizes implementation and local
verification, not publication, infrastructure changes, or live cloud tests.

## 6. Verification and documentation

Focused tests MUST cover:

- A matching version followed by attachment and an ordinary target operation;
  different product versions with the same contract remain compatible.
- Valid mismatch, missing field, null/non-string version, invalid version syntax,
  duplicate keys, trailing JSON, oversized/truncated response, HTTP/envelope status
  mismatch, endpoint 404, server error, connection failure, timeout and cancellation.
- Rejection before attachment intent and target dispatch; no automatic retry,
  no CLI/catalog fallback, and no accepted execution on failure.
- The same fresh check in foreground and supervised preparation, with a failed
  start inspected by a separate CLI invocation and the same safe cause retained.
- Environment/capability or instance validation still rejecting invalid targets
  after a successful compatibility check; HTTPS/offline workflows remaining usable
  without Interceptor when they require no native recovery work.
- Recovery and late collection refusing incompatible native work while exact
  Docker termination and local evidence inspection remain available; no resumption
  or replay, and no false native cleanup success.
- Release pin omission, invalid syntax, platform disagreement, binary/metadata
  disagreement, historical metadata handling, and changed pins during promotion.
  Run the complete reviewed publisher's synthetic catalog tests locally.

Use Operator's existing controlled campaign/process fixtures for the full
check → attach → invoke → inspect evidence → finish → Harness exit → cleanup →
export sequence. Also exercise the client against the actual Interceptor API from
a pinned local build or candidate executable with a small local target. Use the
current local smoke fixture as a reference; do not run another product's complete
qualification suite or require paid model calls. If Docker/native execution is
unavailable, report that exact limitation rather than calling mocked HTTP coverage
full integration qualification.

Update `docs/INTERCEPTOR_CLIENT.md`, `docs/CAMPAIGN_START.md`, relevant preparation
troubleshooting and Operator specification sections, release metadata/installation
documentation, and `.github/maintainers/CANDIDATE_RELEASES.md`. Explain automatic
comparison, mismatch remediation, the distinct environment manifest, both catalog
fields and the publisher upgrade. Campaign instructions MUST NOT add manual
contract export or comparison steps.

Implementation SHOULD use three reviewable commit boundaries: typed discovery and
pin with focused tests; campaign/recovery diagnostics with representative workflows;
then packaged identity/publication integration and documentation. Each boundary
MUST include relevant verification. Report controlled local, native integration,
CI and authorized publication results separately.
