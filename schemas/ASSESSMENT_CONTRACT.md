# Records, conclusions and completion

This fixes concrete fields and validation rules for Shared Contract §§8, 10 and
11. All assessments remain guest assertions. They cannot overwrite host execution
facts, protected evidence, budget counters or final campaign status. Package
publication and broker/harness runtime implementation remain pending.

## Typed records

`engine.record_append` takes exactly `record_kind` and `record` using the
[closed request union](engine-record-append-request.schema.json):

| Kind | Record fields |
|---|---|
| `hypothesis` | Hypothesis ID, objective references, provenance, statement, assumptions, predicted observations and prior record references; optional surface reference. |
| `progress` | Summary, objective/hypothesis/attempt references, observation references and record references; optional decision and typed assessment. |
| `lineage` | Hypothesis ID, committed attempt receipt, mutation summary and record references; optional parent attempt receipt. |
| `coverage` | Coverage entries and record references. |
| `conclusion` | Committed `artifact_receipt` and `finish_reason`. |

Provenance is `scenario` with a required `scenario_id`, or `exploratory` without
that field. Either can name a parent hypothesis. Self-parent hypotheses and attempt
lineage are invalid; other ancestry checks require retained campaign records.
Hypothesis updates append new record receipts rather than rewriting history.

Progress can include an `assessment` with interpretation (`supported`,
`inconclusive`, `not-observed`), confidence (`low`, `medium`, `high`), assurance,
summary and explicit gaps. Confidence is guest judgment, not a measured probability
or host verdict. Supported assessments require observation references. Recording
progress does not grant progress credit or extend deadlines. The one-per-second
progress limit and host-observed progress rules remain runtime gates.

Each record request and response is at most 65,536 encoded bytes including its
ordinary envelope. It uses the ordinary lane and 30-second ceiling. Most reference
lists allow 32 entries; assumptions/predicted observations allow 16 strings of
1,024 characters. Record summaries/statements/decisions allow 4,096 characters.
The schema's field limits do not override the total byte ceiling. Oversized records
fail as a whole; detailed history can use separately committed, referenced records
within campaign quotas.

The [result](engine-record-append-result.schema.json) returns `receipt_id`, matching
`record_kind`, `assertion_origin: harness` and host-supplied `attribution` with
campaign, launch and run revision. No guest attribution fields are accepted in the
request body. The host saves the record/result before returning. An exact operation
duplicate retains its original attribution; earlier worker/revision attribution
never denies same-campaign access. The validator checks campaign/launch/kind
correlation; the ledger distinguishes new records from saved duplicates.

Only conclusion records are eligible during finalization. The host resolves the
artifact, checks purpose/content/binding/finish reason and then commits the record.
The registry marks this operation `record-write`, with durable receipt policy and
`conclusion-only` finalization. `RECORD_REFERENCE_INVALID` covers invalid external
record/evidence references before effects; `CONCLUSION_INVALID` covers invalid
conclusion content. Uncertain persistence remains a terminal unknown outcome.

## Structured conclusion

[EngineConclusion](engine-conclusion.schema.json) uses
`operator.dev/engine-conclusion/v1alpha2` and `kind: EngineConclusion`. It requires
binding, status, finish reason, summary, objectives, hypotheses, claims, coverage,
uncertainties and record references. The [example](fixtures/conclusion-example.json)
uses test identities, not real evidence.

Status is `completed`, `partial` or `failed`. Completed describes the assessment,
not successful exploitation, exhaustive testing or verified claims. A complete
report may honestly include untested alternatives and evidence gaps. Empty arrays
are permitted for bounded partial/failure reports; never invent claims or receipts.

The host compares the [binding](conclusion-binding.schema.json) against its verified
launch records and final revision:

| Field | Identity recipe |
|---|---|
| `campaign_id`, `launch_id`, `run_revision` | Host campaign/launch and final target revision. |
| `input_tree_digest` | Raw bytes of the immutable InputTreeManifest. |
| `engine_context_digest` | Raw bytes of staged EngineContext. |
| `prompt_digest` | Exact effective system-prompt bytes. |
| `skill_set_digest` | Raw bytes of SkillSetManifest, including the empty set. |
| `image_digest` | Full Docker image ID pinned for this launch. |
| `release_record_digest` | Raw bytes of the validated cached HTTPS release response. |
| `contract_package_version`, `contract_package_digest` | Installed stable major.minor.patch version and canonical package-manifest digest. |

These copies grant no authority. Raw-file digests do not replace separately recorded
canonical object digests. Restores keep initial input identities and change the
final revision. Worker IDs and private native bindings are not conclusion fields.

Objectives/hypotheses include IDs, reported outcomes, summaries and attempt/record
references. Hypotheses also carry objective references and provenance. Outcomes
are `supported`, `inconclusive`, `not-observed` or `not-tested`. The runtime checks
objective/scenario membership in the submitted bundle and recorded exploratory
lineage; compact conclusions need not embed every historical hypothesis.

Claims have unique IDs, statements, interpretation, confidence, assurance, attempt
receipts, observations and local uncertainty references. A supported claim requires
an observation; each observation's attempt must appear in the claim's attempt list.
Assurance is bounded to 128 characters and must reflect the referenced source
assurances; there is no invented universal assurance ranking. Receipt resolution
checks availability, visibility and provenance without certifying interpretations.

An observation reference has `attempt_receipt_id`, `entry_id` and optional `range`
with byte `offset` and positive `length`. Omission means the entire stored entry;
an excerpt identifies a half-open raw-byte interval. The end must fit the safe
integer range. The host must additionally check the stored entry length and
visibility. Earlier same-campaign receipts remain valid across restores.

Coverage names an objective, optional hypothesis/alternative, status
`tested`/`untested`/`partial`, reason and attempt receipts. Tested coverage needs an
attempt reference, and the runtime must check that it actually ran. Uncertainties
have unique gap IDs, descriptions, observation/input-entry/record references and
explicit kinds for unresolved effects, unavailable/truncated feedback, unread or
summarized input, omitted ranges, coverage overflow and evidence gaps. Input-entry
references may also specify byte ranges. Claim uncertainty references resolve
within the conclusion.

Bounds: 1 MiB encoded; 8,192-character summary; 100 objectives, hypotheses, claims,
coverage entries and uncertainties each; 256 top-level record references. Claim
statements, entry summaries and gap descriptions allow 4,096 characters; each claim
has at most 32 attempt, observation and uncertainty references. Duplicate objective,
hypothesis, claim or gap IDs are rejected within their respective collections.
History overflow requires a `coverage-limit` gap and partial (or failed) status,
with detailed history retained through committed records.

## Completion-chain validation

The finish sequence remains artifact commit → conclusion record → stop request
with matching finish reason and both receipts → closed-admission/required-exit ACK.

Go `ValidateConclusion` and Python `validate_conclusion` check shape, byte limits,
range arithmetic and internal references. They do not resolve external IDs or
validate the truth of evidence claims.

Go `ValidateCompletion(CompletionInput)` and Python `validate_completion(...)`
check exact conclusion bytes against the host's expected binding, saved artifact
commit response, conclusion-record request/response and stop request. Checks cover
raw SHA-256/byte count, media/purpose, receipt links, campaign/launch/final revision
and finish reasons. An artifact's earlier revision attribution alone does not deny
access; the conclusion itself must match the expected final revision.

Callers must resolve inputs from trusted launch and campaign receipt records.
Guest-supplied messages that agree with each other are not proof of commitment.
Receipt lookup, visibility, stored range bounds, claimed canonicalization,
durable writes, duplicates, admission and shutdown remain runtime requirements.
The helper checks the chain before stop acceptance and neither saves stop nor
claims cleanup/reporting is finished.

An unavailable-conclusion stop still uses ordinary validation and host stop policy;
it never fabricates a committed chain or extends the hard deadline.
