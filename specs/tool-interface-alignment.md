# Model declarations and accepted arguments

The Operator-owned catalog MUST define both runtime argument schemas and model
projections. The interface identified in `release/contract-lock.json` supersedes the former flattened
historical-record and multi-format finish declarations. An old model-tool alias
MUST NOT be advertised or accepted. Native engine operations remain unchanged.

## Tools

- `record_hypothesis` MUST require origin and statement, with scenario_id exactly
  for scenario origin. Its parent MUST identify a hypothesis.
- `record_progress` MUST require summary. Its optional assessment object MUST
  require interpretation, confidence, assurance and summary; gaps belongs there.
  Supported assessments MUST cite inspected observations. Ordinary progress
  MUST remain possible without an assessment or next-step decision.
- `record_lineage` MUST require hypothesis, attempt and mutation_summary. Its
  parent MUST identify an execution.
- `record_coverage` MUST require entries with the shared coverage fields.
- `finish` MUST require finish_reason, summary and objectives. `finish_artifact`
  and `finish_unavailable` MUST expose mutually exclusive completion forms as
  distinct tools using the same native finish lifecycle. Every accepted finish
  ends the campaign; cleanup and assessment success remain distinct.
- `scenario_select` MUST accept only a scenario reference. `reference_read`
  MUST read bytes and reject scenario activation with a specific correction.
  Selecting and executing in one response MUST NOT bypass instruction delivery.

## Rules and presentation

Declarations MUST preserve content choices, origin/setup dependencies, nested
conditional evidence requirements and completed/coverage-limit exclusion.
Injection content MUST expose JSON and JSON artifacts only. Artifact purposes,
inspected citations, execution/hypothesis/snapshot/session handles and UTF-8
byte limits MUST have concise adjacent descriptions. Resource-state validation
MUST remain authoritative; declarations do not grant permission or prove evidence.
Provider projections MUST be tested independently, with any unsupported schema
condition documented beside its arguments. OpenAI and Anthropic MUST use the shared object-root projection without root
`oneOf`, `allOf`, `anyOf` or `not`. Nested rules MUST remain; omitted root
conditions MUST retain concise guidance beside their affected arguments.
Bedrock Converse and Gemini MUST retain the common projection with root rules. The catalog MUST stay frozen during
an admitted conversation. No existing signed history may be rewritten.

## Harness and verification

Harness MUST consume the shared normalization and preserve exact error paths,
limits, handoff corrections, local-only rate-limited handoffs, restricted
finalization and no-replay protections. Summary-only progress MUST not block
ordinary work. A deliberate checkpoint handoff MAY include decision and is
retained in full within existing byte limits, never truncated.

Tests MUST exercise all five codecs, archived invalid combinations and corrected
first-attempt calls. Controlled multi-scenario processes MUST cover progress,
selection, execution, evidence inspection, snapshot/restore and accepted finish.
These checks MUST NOT be described as live-provider qualification.

Provider compatibility tests MUST check the complete catalog, including all finish
and historical-record tools and capability-selected injection variants. Shared
Go/Python fixtures MUST distinguish provider projection acceptance from runtime
argument validation. A live schema-acceptance check MUST send the entire catalog
in one explicitly authorized minimal request, execute no returned tools and avoid
automatic retries. Record the provider/model, exact catalog digest, HTTP result
and bounded usage separately from controlled-test results. This check establishes
schema acceptance, not campaign success or model compliance.
