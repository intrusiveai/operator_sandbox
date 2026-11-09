# Anthropic Messages adapter

Anthropic Messages uses the [model conversation relay](MODEL_CODEC_CONTRACT.md).
Operator constructs request configuration from its private profile. Harness
supplies conversation history and preserves provider-native continuation content.
Provider metadata is not a shared-contract schema. Host token accounting and
Harness tool interpretation validate the fields they consume.

See [provider configuration](../docs/MODEL_PROVIDERS.md),
[accounting and diagnostics](../docs/MODEL_SERVICE.md), and
[context management](MODEL_CONTEXT_CONTRACT.md).
