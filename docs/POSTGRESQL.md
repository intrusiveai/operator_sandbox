# PostgreSQL and pgvector targets

Operator tests applications that use Interceptor's managed PostgreSQL service.
Interceptor owns database startup, TLS, credentials, queries, transactions and
snapshot restore. Operator invokes the application's declared operation; the
Harness does not receive a database connection or an arbitrary SQL tool.

Use the normal campaign submission/start workflow with an Interceptor environment
that declares a reviewed input delivery contract for its application operation.
Export its capabilities and reference the resulting source digest in the scenario
bundle. `service:<id>`, `operation:<id>`, `action:injection:database_response`,
`action:injection:environment_state` and the advertised evidence classes retain
their usual public meanings. The public service includes table-policy IDs, key
columns, writable fields and creation limits, without database passwords, schema
SQL, oracle queries or connection details.

## Permitted mutations

The administrator's target profile must explicitly authorize each injection
route. For example, these entries belong in `scopes.routes` when the native
service is named `database` and its table policy declares `documents`:

```json
[
  {
    "surface": "database_response",
    "target": {"service": "database"},
    "scopes": ["once"],
    "placements": ["replace"],
    "pointers": ["/rows/0/0"]
  },
  {
    "surface": "environment_state",
    "target": {
      "service": "database",
      "collection": "documents",
      "key": "{\"id\":\"one\"}"
    },
    "scopes": ["once"],
    "placements": ["replace"],
    "pointers": ["/body"]
  }
]
```

`database_response` uses simulation mode: it changes an eligible text/JSON cell
in the result seen by the application. It does not mutate stored rows. Its
selector supports the logical service, call ordinal and declared request-body
predicates; HTTP paths/methods and MCP tool names do not apply. Row/column indexes
start at zero. Binary cells and protocol metadata are not mutation targets.

`environment_state` uses fidelity mode: it changes stored data through
Interceptor's table policy. `collection` is that policy's ID. `key` is a JSON
object encoded as a string, containing exactly the declared non-null scalar key
columns. Object member order does not change authorization. A field pointer must
name an allowed writable field. Creating a row requires native `allow_create`, a
positive record limit, an explicitly authorized root replacement, and a payload
whose keys equal the selector and whose other fields are writable. Creation is
not an upsert.

Ordinary application input and small injection payloads can both use direct
content. The compact attempt still requires explicit `origin`; scenario attempts
also require `scenario_id`. An omitted setup list means no injection. Large or
reusable payloads may use the existing committed-artifact path.

Removing an injection definition prevents later application of that definition;
it does **not** undo a committed database change. Restore a suitable checkpoint
when the experiment requires returning to an earlier database state.

## Live feedback and retained evidence

With native and effective `oracle-assisted` feedback, permitted
`database_query_match` verdicts are available through the ordinary attempt result
and `observation_read`. Select `oracle_outcome` or use the all-permitted default.
Operator exposes only the positive verdict and an opaque detector handle, not
its SQL, baseline or matched rows. Black-box and diagnostic feedback do not
expose these oracle verdicts.

Verdicts belong to the observed turn/attempt boundary. Later writes and restores
do not change previously retained observation bytes. An empty category means no
positive verdict was captured, not proof of attack failure. An unavailable or
partial category, including `oracle_evaluation_unavailable`, must remain unknown;
available entries in a partial category are still readable. Other state oracles
retain their documented post-run restrictions.

Snapshots include Interceptor's database backup and application state. Restore
keeps the Harness process/conversation and adopts the replacement native session.
Operator verifies backup digests and sizes in both current and restore-source
journals when collecting archives. Missing bytes are an evidence gap, never
reconstructed from a running database.

Reports show permitted observation excerpts and selected native database event
facts with session, sequence and digest references. Live attempt verdicts and
session-wide final evaluation are labeled separately. Observing an event during
an attempt does not establish that its injection caused the event. Complete
native archives remain available to the administrator through the report's links.

## Attack Harness compatibility

Build the Harness with the exact package version and digest in Operator's
[release contract lock](../release/contract-lock.json), including its generated
model-tool catalog and Python validators. The image's embedded compatibility
record must identify that package. Operator's existing local image checks reject
an older package before campaign execution; no additional compatibility endpoint
or protocol negotiation is used.

The Harness uses the existing attempt, observation and finish tools. Database
mutations use the optional setup actions described above. Guidance should keep
live oracle verdicts distinct from target output and explain that deleting a
state injection does not undo its committed mutation. A new database-specific
tool or database credential is not needed.

## Local integration verification

Build a native Interceptor archive containing the database export fix. Have a
native `python:3.12-slim` image available to Docker, prepare Operator's test Python
environment (`make setup`), and provide the sibling Attack Harness source checkout.
With no Interceptor already listening on localhost port 8080, run:

```sh
python3 scripts/qualify_postgresql.py \
  --interceptor-release /absolute/path/to/interceptor-release.tar.gz \
  --output /absolute/new-test-output
```

This opt-in test builds an isolated PostgreSQL target, uses controlled local model
responses, and runs the production Python Harness entrypoint against Operator over
file-spool transport. It exercises injections, live evidence reads, database
restore, finish and archive/report generation. It removes its test containers and
image tags. The output retains the report and test logs. This is a real native
target/process integration test, not a paid cloud campaign or qualification of
the Harness Docker image on every supported host platform.

See the [implementation contract](../specs/postgresql-integration.md),
[feedback rules](FEEDBACK_PROJECTION.md), and
[shared package distribution](CONTRACT_PUBLICATION.md).
