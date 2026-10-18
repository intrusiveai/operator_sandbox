# Fixed model-tool catalog

The shared package MUST own the model-facing tool names, argument schemas and
native provider projections. `model-tool-arguments.schema.json` is generated from
installed wire schemas by `scripts/generate_tool_catalog.py`; generated files MUST
be checked for drift before publishing a package. Operator and Attack Harness MUST
use the same verified package to derive tool declarations and their digest.

## Declarations and dispatch

`Protocol.ModelTools` (Go) / `Protocol.model_tools` (Python) MUST accept an installed
codec ID and a sorted, unique, known operation set from verified startup context.
A tool MUST be declared only when every operation in its
`x-operator-required-operations` annotation is available. These annotations belong
to the installed schema, never to model output. All five native projections MUST
preserve the same names, descriptions and argument semantics. JSON Schema references
MUST be resolved from the installed catalog before declarations reach a provider.

### OpenAI object declarations

Both OpenAI codecs MUST emit `type: object` parameters with no root `oneOf`,
`anyOf`, `allOf`, `enum`, `const` or `not`, including with `strict: false`.
The generated catalog's `x-operator-openai-parameters` annotation supplies the
reviewed provider declaration for `attempt_execute` and `record_append`; both
language implementations MUST use it for Chat Completions and Responses. Azure
OpenAI and compatible routes using those codecs receive the same declarations.
Other codecs MUST retain their existing projections.

`attempt_execute` MUST retain its closed properties, required fields and nested
constraints. Its description MUST state the cross-field rules enforced at runtime:
scenario origin requires a scenario ID and exploratory origin forbids one;
parented generations are at least two and unparented generations equal one;
carrier input requires a carrier; setup actions require a canonical JSON payload.

`record_append` MUST declare a closed object with required `record_kind` and
`record` properties. `record_kind` MUST enumerate the four supported kinds;
`record.anyOf` MUST contain their complete body schemas, with each body labeled
with its kind. The description MUST require the kind and body to match.

Provider declarations are generation guidance and MAY accept a broader set of
cross-field combinations. The original `properties.arguments` schema MUST remain
the authoritative runtime validator, including the complete attempt `allOf` and
record `oneOf`. `ValidateToolArguments` / `validate_tool_arguments` MUST NOT validate
against the broader provider annotation. A mismatched record MUST be rejected
before its handler; an invalid attempt MUST be rejected before artifact lookup or
target contact, after allocating its nonrefundable submission index. Tool names,
argument shape and wire operations MUST remain unchanged.

Publication MUST rebuild Operator and Attack Harness against the same revised
contract package. Existing images with the older embedded Python projection MUST
be rebuilt; neither compatibility pins nor declaration-digest checks may be bypassed.

The runtime MUST dispatch through a fixed handler map and independently check the
advertised set, live campaign binding, quotas and validated arguments. A declaration
or a valid argument object MUST NOT create execution authority. Raw model relay,
artifact chunking and shell execution MUST NOT be exposed as model tools.

| Tool | Model arguments and handler responsibility |
| --- | --- |
| `reference_read` | Verified reference handle, offset and bounded byte count. The runtime MUST resolve handles through its verified input index; arguments MUST NOT select filesystem paths. |
| `artifact_publish` | Purpose, media type and inline UTF-8, base64 or JSON data. The runtime MUST enforce byte limits, validate encoding and publish through begin/part/commit before returning a receipt. JSON values MUST use canonical encoding; a JSON string MUST remain a JSON string. |
| `attempt_execute` | Tactical attempt fields. The runtime MUST supply API version, kind, release identity, request ID, attempt ID and submission index. |
| `injection_delete` | Known attempt receipt and action handle, using the ordinary deletion contract. |
| `observation_read` | Receipt-scoped observation entry and bounded range, using the ordinary visibility contract. |
| `record_append` | Typed hypothesis, progress, lineage or coverage record. A model MUST NOT append a conclusion through this tool. |
| `request_stop` | Structured conclusion draft. The runtime MUST supply verified bindings, validate and publish the conclusion, append its record and complete the ordinary stop handshake. |
| `restore_request` | Known checkpoint identity. Successful restoration MUST skip later calls in the current model response with correlated revision-change results. |
| `snapshot_request` | Snapshot label/description, with campaign metadata supplied by the host. |
| `snapshot_list` | Bounded listing/pagination fields. |
| `snapshot_inspect` | Source-session/checkpoint identity. |

The artifact publication tool requires all three upload operations. The stop tool
requires those operations plus record append and stop. Reference reads are local
and require no ordinary host operation. Other tools require their corresponding
ordinary operation.

## Argument validation and lifecycle

`ValidateToolArguments` / `validate_tool_arguments` MUST validate a bounded object
against the fixed tool's closed argument schema. The caller MUST first enforce
configured per-call limits. For a new attempt, the runtime MUST allocate its
bookkeeping tuple after bounded object decoding and before argument/semantic
validation; subsequent rejection MUST NOT refund the attempt index.

Local shape validation MUST NOT replace ordinary wire validation or receipt checks.
The runtime MUST retain mappings from model call IDs to local allocation and host
receipts so malformed calls, restore-skipped calls and completed calls receive
exactly correlated results. Conclusion construction MUST follow
[the completion contract](ASSESSMENT_CONTRACT.md), including host-bound identity
and the precise stop acknowledgement. Finalization MUST remain available through
the runtime's bounded terminal path even when ordinary model dispatch ends.

## Validation

Go and Python MUST run `fixtures/model-tools.json`, checking exact canonical
projection digests/byte counts, dependency selection, reserved fields and malformed
arguments. Every native projection MUST pass the corresponding full model request
validator. Tests MUST verify returned data cannot mutate the installed catalog.
These tests qualify local contract agreement; provider/runtime qualification MUST
still exercise actual startup and model-tool execution.
Fixtures MUST distinguish provider-schema acceptance from authoritative runtime
acceptance for cross-field cases. Integration tests MUST exercise invalid calls
followed by valid calls through the real Harness dispatcher and confirm that only
valid calls have effects and rejected attempt indexes are not reused.
