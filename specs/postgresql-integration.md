# Interceptor PostgreSQL and pgvector integration

## Outcome and scope

Operator MUST support campaigns against Interceptor applications backed by its
managed PostgreSQL and pgvector service. A matching Attack Harness MUST be able to
invoke an application with direct input, inspect permitted feedback, optionally
use authorized database injections and snapshots, and submit one finish request.
Operator MUST retain verifiable database evidence and report the final execution
and cleanup outcomes.

Interceptor owns database packaging, startup, synthetic application credentials,
TLS, seeds, database connections, backups, restore, and shutdown. Operator MUST
reuse its existing application invocation, injection, snapshot, restore, evidence
collection, and cleanup paths. This integration MUST NOT introduce a separate
database service, host database dependency, Harness database connection, or
arbitrary SQL execution tool.

An authorized Harness MUST be able to inspect recorded database oracle outcomes
after a completed turn and before its next attempt, through the existing
`oracle_outcome` feedback category. Session completion or export MUST NOT be a
prerequisite. This uses captured evidence, not a new query against the current
database. Private SQL and raw oracle query results remain outside public feedback.

## Sources and ownership

Implementers MUST read the current source alongside these requirements:

- Interceptor: `../interceptor_sandbox/specs/postgresql-integration.md`,
  including its live-feedback amendment, `../interceptor_sandbox/docs/postgresql.md`,
  `../interceptor_sandbox/docs/feedback.md`, and the PostgreSQL examples.
- Interceptor's delivery, observed-turn evidence, and application-state snapshot
  contracts, referenced by its PostgreSQL specification.
- Operator: [capability export](../schemas/CAPABILITY_EXPORT_CONTRACT.md),
  [ordinary execution](../schemas/ORDINARY_WIRE_CONTRACT.md),
  [feedback](../schemas/FEEDBACK_CONTRACT.md),
  [shared contract](../schemas/SHARED_CONTRACT.md), and
  [contract publication](../docs/CONTRACT_PUBLICATION.md).

Operator owns the shared schemas, semantic rules, Go/Python validators, and
fixtures. Attack Harness MUST consume the resulting exact package identity.
Native Interceptor records MUST remain faithful to Interceptor's serialization
and integrity rules. An implementer MUST resolve source/contract disagreements
explicitly rather than relaxing validation or inventing a parallel protocol.

## 1. Capability ingestion and public projection

Operator MUST accept and validate the native database capability additions:

- Service kind `postgresql` and transport `postgresql+tls`.
- Injection surface `database_response` and its declared scopes, placements,
  selector fields, and `postgresql_result` carrier.
- Oracle type `database_query_match`.
- `database_tables`, including table policy ID, key columns, writable fields,
  creation permission, and applicable record limit.

Update `internal/capabilities/native_types.go`, native validation, injection
vocabulary, public projection, and the public capability schema together. Native
typed serialization MUST preserve field order and omission rules used by
Interceptor's digest computation. Unknown or malformed fields MUST continue to
fail validation. The public transport identifier rule MUST accommodate the
declared PostgreSQL transport without broadly weakening unrelated identifiers.

The public export MUST retain the table policy information needed to select
authorized injections, with deterministic ordering and digest binding. It MUST
NOT expose database credentials, private management endpoints, or sealed oracle
SQL. Capability presence MUST NOT grant an injection route by itself; installed
target policy and submitted campaign scope continue to constrain admission.

Add a captured native export → public capabilities → admitted submission fixture
using Interceptor's actual exporter. Cover services with response-only injection,
existing-record mutation, and explicitly permitted record creation, as well as
unchanged non-database environments.

## 2. Target operation descriptions and examples

The PostgreSQL examples use Interceptor's `code` application adapter. Operator
MUST accept that adapter through the existing native invocation mechanism and
retain explicit input-delivery validation. This does not authorize loading or
executing the target's adapter code in Operator or Attack Harness.

An Operator-ready example MUST supply a reviewed delivery description for its
application operation, including JSON input shape, media type, and size limits,
using Interceptor's delivery-capable Blueprint format. Operator MUST NOT infer
that description from example input or silently admit an undescribed operation.

Provide a reproducible local example with a seeded database, precomputed vectors,
a deterministic retrieval operation, and an objectives/scenarios bundle. Document
the target profile, permitted injection routes, feedback profile, and commands
for capability export, submission, execution, inspection, and reporting. Database
assets and application startup remain Interceptor responsibilities. Coordinate
any required changes to its examples explicitly with that repository.

## 3. Database injection admission and translation

Extend the shared execution schema, normalized attempt plan, generated model-tool
definitions, and `internal/attemptadapter` consistently. Ordinary attempts MUST
retain explicit origin, direct input or artifact-receipt input, product-owned
bookkeeping, and optional setup actions. No hypothesis preregistration or separate
conclusion publication sequence may be introduced.

### Response injection

Operator MUST admit `database_response` only for an authorized PostgreSQL service,
using Interceptor's existing simulation-mode response injection semantics.
Selectors MUST support its documented call ordinal and request-body predicates
over query, parameters, and result index. HTTP routes and methods MUST NOT be
required for database response selection.

Placements MUST respect installed pointer/placement permissions and native result
constraints. The result root is the captured rows/columns representation, with
eligible text and JSON cells; changing row counts, column metadata, NULL semantics,
or unsupported native types MUST remain rejected by the appropriate validator.
Native vector query execution MUST remain native; Operator MUST NOT implement SQL
or vector operations itself. Interceptor remains authoritative for result-dependent
validation and delivery confirmation.

### State injection

Operator MUST resolve `environment_state` database targets against declared table
policies, not HTTP endpoints or creation-enabled collection lists alone. Mutation
of an existing eligible row MUST be possible when record creation is disabled.
Writable fields, key columns, creation permission, and limits MUST constrain
admission and remain enforced natively.

The current native `selector.key` is a string containing a JSON object. Document
this representation with working examples and validate it as a bounded record
key: every declared key column, no extra keys, and non-null scalar values.
Database keys MUST NOT inherit filesystem-path restrictions. Matching authorized
keys MUST compare validated values deterministically rather than depending on
JSON whitespace or object member order. Any normalization MUST preserve values
and MUST NOT repair invalid supplied keys.

Record creation MUST require explicit permission and the native root-replace
semantics; it MUST NOT become an implicit upsert. Injection removal disables or
removes the injection definition but MUST NOT be reported as undoing a committed
database mutation. Document checkpoint restore as the existing mechanism for
returning database state to a saved baseline.

## 4. Native evidence and snapshot integrity

Extend `internal/interceptor` evidence types and verification to recognize
`database_checkpoint` operations with the exact native allowed fields. Verify
their service binding, metadata shape, backup digest, and byte count according to
the native contract. Referenced backup blobs MUST receive the existing integrity
checks for both current and restore-source journals. Missing, corrupt, oversized,
or inconsistent evidence MUST fail explicitly; accepting a new kind without
verifying its referenced content is insufficient.

Align native attribution decoding and digest reconstruction with Interceptor's
`window`, `observed_attempt_id`, and `observed_payload_digest` additions. Preserve
the distinction between observed association and confirmed causal attribution.
Older records with omitted optional attribution fields MUST remain verifiable.
Retain strict record decoding, hash chains, checkpoint lineage, and blob checks.

Database query/result/error/mutation/oracle events and their raw blob references
MUST survive verified native evidence collection and export. Operator MUST NOT
translate an unconfirmed transaction outcome into a committed or rolled-back
outcome. Database evidence capture failures MUST retain the existing terminal
failure and no-replay behavior.

Reuse existing snapshot commands and cumulative count/byte allowances. Include
native database backup charges in the existing accounting without resetting
Operator's cumulative consumption on restore. Interceptor coordinates idle pools,
open transactions, application state, database engine compatibility, and restore.
Operator MUST preserve the existing distinction between preflight rejection and
terminal failure after closure, and MUST NOT restart a healthy Harness merely
because a snapshot was restored.

## 5. Feedback, reporting, and diagnostics

### Admission, selection, and normalized contents

Operator MUST collect database oracle outcomes through the existing native
`observation.read` and `observation.content.read` operations and expose permitted
bytes through its existing receipt-scoped Harness observation reads. No new
feedback channel, database query tool, or completion handshake is required.

Keep existing feedback profiles and visibility checks. The native session MUST
enable `oracle-assisted` to collect oracle outcomes; native attempt registration
MUST continue to use the session profile. Operator MUST independently enforce the
Harness's effective profile, including bundle narrowing. Black-box and diagnostic
effective profiles MUST withhold oracle outcomes even when the native session
permits them. Default all-permitted selection includes `oracle_outcome` when
authorized; explicit selection MUST include it to request these outcomes.

Extend `internal/feedback/projection.go` normalization to accept
`database_query_match` alongside the existing supported oracle kinds. Preserve
the native positive record (`oracle_id`, `oracle_kind`, `fired: true`) through
Operator's existing normalized representation (`detector_id`, `detector_kind`,
`fired: true`), including its session-scoped opaque detector handle. Update shared
documentation, validators, and fixtures wherever those kinds are constrained.
Use an explicit field projection; SQL, private query results, oracle definitions,
credentials, and unrelated native metadata MUST NOT enter Harness feedback.

### Evidence boundaries and availability

Live outcomes describe already captured application observations bound to the
requested turn and attempt through that turn's recorded completion boundary.
Operator MUST retain and verify the existing receipt/source bindings, content
integrity, visibility, and byte-range limits. Interceptor owns filtering and
evaluation of its native events; Operator MUST NOT reconstruct a live verdict
from the current database, a session-wide report, or a cumulative/latched result.

Earlier/later turns, other attempts, late/outside/ambiguous observations, seed or
Harness changes, and inherited checkpoint verdicts MUST NOT substitute for the
requested turn's evidence. A response rewrite, rollback, or successful COMMIT
alone is not a business-outcome verdict. A positive record establishes that the
condition was observed, not that this attempt or its injection caused it.
Live and retained evaluation of the same eligible evidence MUST agree; a
session-wide historical report can legitimately contain additional outcomes.

Retained receipt reads MUST remain unchanged after subsequent writes or restore,
using the existing historical receipt access rules. Reading an old receipt MUST
NOT re-evaluate it against replacement session state or rebind its detector
identity to the new session.

Preserve category state as well as individual entries. A complete nonmatching
observation can yield an empty category; absence of a positive result is not
proof of attack failure. Missing, corrupt, incomplete, or ambiguously attributed
required evidence MUST remain unavailable/partial, never a false verdict or a
complete empty result. Valid outcomes from other oracles MUST remain readable
when the category is partial.

Update the bounded reason projection to preserve `oracle_evaluation_unavailable`
and `no_positive_attributed_result_not_proof_of_failure`, and preserve
`state_oracles_require_post_run_evidence` only when reported for other unsupported
state-oracle kinds. Database oracles MUST NOT receive that restriction merely
because they concern state. Unknown reasons MUST still use a safe fallback;
unrestricted native errors MUST NOT be forwarded. Retain explicit capture-limit
and content-unavailable handling when Operator itself cannot retain feedback.
Evidence persistence failures MUST preserve existing fail-closed execution and
cleanup behavior.

### Reporting and diagnostics

Database raw events in an administrator evidence archive MUST NOT automatically
become Harness-visible observations. Enhance the existing report/evidence
projection using verified retained facts:

- Identify database-backed services and associated attempts where evidence
  supports that association.
- Summarize database injection applications/failures, receipt-bound live oracle
  observations, and retained session-wide verdicts, with references to the
  corresponding receipts and complete native evidence. Identify their different
  observation boundaries rather than presenting them as interchangeable.
- Distinguish application traffic, Harness mutations, observed windows, confirmed
  delivery, transaction outcomes, and missing or truncated evidence.
- Preserve execution coverage versus objective achievement, Harness assessment
  versus observed facts, and execution versus cleanup outcomes.

Summaries MUST use bounded, explicitly selected fields and render untrusted
content as data. They MUST NOT expose credentials or management secrets, claim
causality from temporal association, or require another model call. Native SQL and
result evidence MUST remain subject to existing archive access and retention
rules; do not copy unrestricted query contents into generic diagnostics.

Capability, selector, policy, snapshot, and evidence failures MUST identify the
failed operation/rule and relevant bounded identifier using existing diagnostics.
Preserve primary failures through cleanup, and retain secondary cleanup failures
separately. No automatic retry, replay, or campaign resumption is permitted.

## 6. Shared contract and component coordination

Update affected capability, execution, feedback, and model-tool documentation
alongside code. Regenerate derived schemas/catalogs and publish a new shared
package version and exact digest through the existing package tooling. Select the
package version according to the actual compatibility change; do not derive it
from a product release number.

Attack Harness MUST consume that package, expose only capability-applicable
database actions, and describe valid selectors, references, and state cleanup
semantics. Its guidance MUST explain how to inspect permitted database oracle
feedback before choosing the next attempt, and distinguish positive observations,
empty results, unavailable/partial evidence, and causal claims. Verify supported
provider projections against the same accepted
interface. Existing local image compatibility validation MUST reject incompatible
combinations before execution. Do not add runtime schema negotiation or release
service admission lookups.

Coordinate the exact contract, native example prerequisites, and matching Harness
changes before the process-level verification. If matching components are not
available, report that verification as incomplete rather than claiming integration
success from schema tests alone.

## 7. Verification and implementation boundaries

Use these reviewable implementation boundaries, with focused tests at each:

1. Capability ingestion/projection, code-adapter admission, and real native export
   fixtures, including malformed policies, unknown fields, digest tampering, and
   non-database regression cases.
2. Shared injection schemas, policy/translation, generated provider projections,
   and matching Go/Python fixtures. Cover composite keys, equivalent key encoding,
   unauthorized fields/tables, existing-row mutation, forbidden creation, and
   response selector/placement failures before any inappropriate dispatch.
3. Evidence/checkpoint/attribution verification, live feedback projection, and
   reports. Cover current and restored journals, corrupt/missing backup blobs,
   size allowances, old records, and receipt-bound versus session-wide oracle
   evidence without false causality. Use actual native feedback fixtures for
   positive, complete nonmatching, unavailable, and mixed partial categories.
   Test permitted/withheld/unrequested feedback, effective-profile narrowing,
   unknown/malformed oracle kinds, private-field removal, and supported reason
   projection. Include earlier/later turns in one attempt, other attempts,
   late/outside/ambiguous events, and unchanged restrictions on other state oracles.
4. Matching Operator–Harness process verification, documentation, and final
   package identity checks.

The representative verification MUST use real local Interceptor database execution
and real Operator–Harness process/transport boundaries, with controlled model
responses. It MUST demonstrate:

- A scenario-associated direct-input invocation using seeded vector retrieval,
  permitted observation inspection, one accepted finish, verified Harness exit
  code zero, owned cleanup, and inspectable Markdown/exported evidence.
- Response injection changes the eligible delivered result without mutating its
  stored row; state injection changes a permitted row and respects creation limits.
- Snapshot → mutation → restore returns database/application state and oracle
  baselines correctly, preserves the Harness, and does not replay application work.
- Idle pool snapshot behavior, open-transaction rejection, cumulative backup-byte
  limits, and restoration of retained injection state and cleanup semantics.
- A committed application business outcome is obtained through live
  oracle-assisted feedback and read by Harness before export or finish; a
  subsequent attempt executes while the same session remains running. Compare
  the live outcome with retained evidence at the same completion boundary.
- Rollback, seed state, response injection alone, and Harness-only writes do not
  manufacture a positive business outcome. Missing/corrupt required evidence and
  storage failures are explicit, including mixed valid/unavailable oracles.
- Re-reading an old receipt after later writes and snapshot restore returns the
  same result. New turns do not inherit an old positive verdict as their own;
  session-wide historical evidence remains distinguishable and verifiable.
- Rejected invalid requests do not dispatch; uncertain effects, missing evidence,
  interrupted execution, and cleanup failures remain explicit and do not replay.
- Archives, reports, diagnostics, and retained facts remain inspectable after
  container removal, and incompatible component identities fail before execution.

Use temporary local databases and deterministic fixtures. No paid model calls,
cloud writes, or release publication are required or authorized by these
implementation instructions. Record exact tested component/contract identities,
observed results, and remaining limitations; distinguish controlled verification
from live cloud qualification. Run the repository's applicable generated-contract,
Go/Python, evidence, reporting, and integration checks before delivery.
