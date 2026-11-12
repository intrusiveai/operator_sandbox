# Fixed tool declaration measurement

Canonical UTF-8 tool bytes, measured with `Protocol.model_tools(codec, operations,
surfaces)`, comparing source `ee741e2bd6b949b61ae4e0151b54be907fc22a98` with the
presentation defined in [shared tool presentation](../tool-context-presentation.md).
The operations are the full set in the first projection case of
`schemas/fixtures/model-tools.json`. Disabled means an empty surface selection;
enabled means all five admitted surfaces. All other selections are identical.

| Codec | Disabled before | Disabled after | Enabled before | Enabled after |
| --- | ---: | ---: | ---: | ---: |
| OpenAI Chat | 14,957 | 13,726 | 24,938 | 23,908 |
| OpenAI Responses | 14,814 | 13,583 | 24,795 | 23,765 |
| Anthropic | 14,473 | 13,242 | 24,454 | 23,424 |
| Bedrock | 14,704 | 13,473 | 24,685 | 23,655 |
| Gemini | 14,588 | 13,357 | 24,569 | 23,539 |

These measure only recurring tool declarations, excluding trusted prompt, current
task, conversation history and deferred guides. They are not tokenizer counts or
live-model performance measurements. Deferred guidance grows to preserve detailed
instructions outside recurring declarations. Required arguments and runtime and
provider constraints are unchanged; no ordinary workflow call has been added.

The existing `scripts/measure_tool_restructuring.py` separately regenerates
`schemas/fixtures/tool-context-cost.json` against its older restructuring baseline.
Go's `TestModelToolContextNonGrowth` checks those current byte counts. The selected
model-tool fixtures check the same canonical projection bytes in Go and Python;
`TestSelectedToolPresentationAtProviderBoundary` checks actual local HTTP requests.
