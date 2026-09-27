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
