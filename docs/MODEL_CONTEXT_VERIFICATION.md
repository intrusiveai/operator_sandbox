# Model context verification

Controlled local tests use the production Python entrypoint, Go host service,
shared package, and real FIFO/file-spool transports. Provider and target responses
are local fixtures. These checks do not qualify live providers or Docker containment.

The warning workflow performs one resource-detail read, two direct scenario
attempts, one observation read, one compaction and one finish. Five model calls
include compaction. Receipt identities and scenario coverage survive. The exported
report retains the 8,200-input-token warning and all five request-size measurements.

## Provider declarations

Canonical JSON bytes, not token counts. No-injection measurements retain all
operations to isolate setup-branch savings; HTTPS also removes unsupported tools.
Shared fixture tests cap each selected catalog at 31,000 bytes.

| Codec | Previous full | Current full | No injection setup |
| --- | ---: | ---: | ---: |
| openai-chat-text-tools-v1 | 41570 | 29925 | 21939 |
| openai-responses-text-tools-v1 | 41427 | 29782 | 21796 |
| anthropic-messages-text-tools-v1 | 41184 | 29184 | 21198 |
| bedrock-converse-text-tools-v1 | 41415 | 29415 | 21429 |
| gemini-text-tools-v1 | 41299 | 29299 | 21313 |

Bedrock components, excluding wrappers:

| Tool | Previous description / schema bytes | Current description / schema bytes |
| --- | ---: | ---: |
| artifact_publish | 126 / 810 | 222 / 729 |
| attempt_execute | 322 / 20077 | 417 / 11074 |
| finish | 253 / 8621 | 339 / 6647 |
| injection_delete | 74 / 705 | 170 / 563 |
| observation_read | 86 / 481 | 282 / 342 |
| record_append | 116 / 6742 | 209 / 5288 |
| reference_read | 109 / 371 | 283 / 303 |
| restore_request | 120 / 378 | 215 / 236 |
| snapshot_inspect | 76 / 378 | 172 / 236 |
| snapshot_list | 69 / 331 | 162 / 260 |
| snapshot_request | 93 / 194 | 189 / 194 |

## Target index

| Shared fixture | Full / compact bytes | Detail references |
| --- | ---: | ---: |
| capability-chain | 4767 / 3633 | 5 |
| postgresql-capability-chain | 3209 / 3428 | 5 |
| wide-table-context | 3925 / 3428 | 5 |

Small targets can grow slightly because exact reference handles have a fixed cost.
The compact view defers full contracts without discarding them. Reads neither
regenerate tools nor grant authority. Runtime validation retains every constraint.

Verification includes shared codec budget parity, invalid usage and read bounds,
modified references, repeated compaction identities, exhausted budgets, large
startup inputs and skills, restore batch boundaries and PostgreSQL observations.
Exact token preflight stays disabled pending separate counter qualification.
