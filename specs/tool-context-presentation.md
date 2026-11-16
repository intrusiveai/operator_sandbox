# Shared tool presentation

## Ownership and compatibility

Operator MUST generate the authoritative model-facing declarations and deferred
help from `scripts/generate_tool_catalog.py`. Go and Python MUST expose the same
selected declarations. Harness MUST consume them without maintaining a second
catalog or rewriting their semantics.

Presentation changes alter the exact interface bytes. Regenerate and review
`schemas/asyncapi.json`, then approve its identity in
[`release/contract-lock.json`](../release/contract-lock.json) using the existing
contract procedure. A matching Harness build MUST explicitly adopt that identity.
Do not introduce runtime negotiation or a second compatibility check.

## Recurring declarations and deferred guidance

- `attempt_execute` MUST describe permitted invocation, explicit origin/scenario
  association, and the exactly-one content choice. Setup, injection content and
  retention guidance MUST accompany their selected properties; declarations
  without those properties MUST NOT instruct the model to use them. Full media,
  artifact-purpose and parent-handle rules MUST remain discoverable in its guide.
- `finish` MUST say acceptance ends the entire campaign, not the active scenario.
  It MUST distinguish acceptance from confirmed cleanup and retain correction
  without target replay. Minimal inline examples, enum lists, uncertainty labels,
  handle categories, artifact and unavailable alternatives MUST remain in deferred
  guidance. The ordinary description MUST NOT repeat a zero-attempt example.
- `reference_read` MUST explain that selecting a scenario at offset zero activates
  its complete instructions in the current task and returns a short confirmation,
  without pagination. Other references retain byte reads and continuation. This
  declaration MUST explain `tool-guide:<tool_name>` discovery once; other tool
  descriptions SHOULD NOT repeat it.
- `observation_read` MUST distinguish readable handles from inspected citation
  handles and explain continuation. Its guide MUST include a working pagination
  example and limitations for partial, truncated and unavailable evidence.
- `record_append` MUST identify historical recording as optional. Its guide MUST
  preserve progress-assessment field relationships; ordinary progress MUST NOT
  require a historical record.
- Restore guidance MUST preserve the rule that successful restore skips later
  calls in the same model response.

Generator changes MUST preserve accepted arguments, bounds, enum values,
relationships, diagnostic corrections, provider policies and frozen capability
selection. Scenario prose MUST NOT hide otherwise admitted capabilities. Avoid
repeated runtime-limit boilerplate and obsolete-interface tutorials in ordinary
instructions; actual validation errors MUST still explain rejected arguments.

## Advanced database guidance

The deferred attempt guide MUST retain database selector and lifecycle semantics:
resource details discovery, permitted service/table, exact key columns, writable
columns, row-creation permission and limits; admitted surface/mode/placement;
query call ordinals/predicates and eligible cell replacements; persistent state
mutation versus response replacement; removal that does not undo committed
mutations; and admitted ready-snapshot restore. It MUST NOT authorize unsupported
native types or changes to row count, column metadata or NULL semantics. This
material MUST remain discoverable without becoming universal invocation prose.
The observation guide MUST preserve database oracle limits: a positive verdict is
an observed condition rather than injection causality, empty is not proof of
failure, and old receipts or session-wide reports are not new-attempt results.

## Verification

Tests MUST inspect actual provider requests for Chat Completions, Responses,
Anthropic, Bedrock and Gemini, with injection disabled and selected surfaces
including database/state. Shared fixtures MUST establish Go/Python byte parity.
Existing malformed-field, duplicate-key, reference, range and finish validation
MUST continue to reject invalid submissions.

Use the matching Harness process with controlled provider and target responses to
verify scenario selection followed immediately by action, observation inspection,
a second scenario, snapshot/restore, and an honest campaign-wide finish retaining
prior citations. Verify acceptance, exit code zero, owned cleanup and export.
Distinguish this controlled process test from live providers and Docker containment.
Measure fixed tool bytes separately from task, prompt, history and deferred help;
do not present byte estimates as measured model tokens.
