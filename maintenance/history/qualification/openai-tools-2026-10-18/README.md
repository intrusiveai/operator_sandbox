# OpenAI object-schema verification

> Historical engineering record. This captures the instructions and evidence at
> the time it was written; it is not the release integration contract.

Date: 2026-10-18 UTC. Model: `gpt-5-mini`. Provider: direct OpenAI API.

Four live requests advertised all eleven tools from the production Python
`Protocol.model_tools` implementation. Each explicitly selected one affected tool,
provided synthetic arguments, disabled parallel calls, and capped output at 1,024
tokens with minimal reasoning. There were no retries or target/tool executions.
The API key was read only in memory from an administrator-provided local file;
no credentials or raw provider responses are retained here.

| API | Selected tool | HTTP | Full runtime validation | Input tokens | Output tokens |
| --- | --- | --- | --- | --- | --- |
| Chat Completions | `attempt_execute` | 200 | Passed; exact requested arguments | 10,900 | 118 |
| Chat Completions | `record_append` | 200 | Passed; exact requested arguments | 10,869 | 93 |
| Responses | `attempt_execute` | 200 | Passed; exact requested arguments | 10,896 | 113 |
| Responses | `record_append` | 200 | Passed; exact requested arguments | 10,865 | 102 |

These are observed usage counts, not token reservations. Input counts include
cached tokens where reported. The two record calls each reported 9,856 cached
input tokens. Total observed output: 426 tokens.

## Exact declaration identities

All operations were enabled. Canonical tool arrays sent to the provider:

- Chat Completions: 58,000 bytes,
  `sha256:ace7da1e99d0dc197dad729b479267c1c3674ef5b27a20900e3813f0a6a5631b`.
- Responses: 57,857 bytes,
  `sha256:6d43b72d94ae1c8c18c5fb3982ed8f499b55b604d02ae160c6992d08d11dd3e0`.
- Generated model-tool schema bytes:
  `sha256:59dd92ce5124538ec83bdfa0649905d0acab9b9e3232cbe613b31549c2dc583b`.

The matching identities are covered by `schemas/fixtures/model-tools.json` in
both Go and Python. The original authoritative argument schemas retain every
attempt conditional and record-kind constraint. The provider projection exposes
ordinary object roots, with the record-body union nested under `record`.

## Local workflow verification

Shared fixtures check valid/invalid scenarios, parent/generation rules, carrier
requirements, canonical setup payloads, all four record kinds, wrong kind/body
combinations, missing/unknown fields, and nested evidence constraints. They
explicitly distinguish broader provider-schema acceptance from runtime rejection.

`TestPythonProcessToolSchemaRejections` runs the production Harness entrypoint
against Operator over FIFO and spool with controlled model/target peers. It
rejects a missing scenario ID and a mismatched record locally, then accepts a
valid record and attempt, concludes, and reports the successful attempt with
submission index 2. The invalid attempt never contacts the target and its index
is not refunded. Existing campaign slices cover ordinary valid execution.

```sh
go test ./contracts -run 'TestSharedModelTools|TestOpenAIProjectionAndRuntimeConstraints' -count=1
PYTHONPATH=contracts/python .venv/bin/python -m unittest discover -s contracts/python/tests -p test_model_tools.py
OPERATOR_HARNESS_INTEGRATION=1 go test ./internal/preparation -run 'TestPythonProcessToolSchemaRejections|TestPythonProcessCampaigns' -count=1
```

## Deployment and limits

The release pin selects contract package `0.0.5`, digest
`sha256:3b5690013ca4e0bb5f7e7c904b26df401f113ec61ef6158ad948db337ef13198`,
from Operator source commit `1eaca1da7ad236353cb86d8799c93c679389ca4f`.
`release/contract-lock.json` contains the full source tree and package metadata.

Build Operator and Attack Harness with the same updated contract package and
publish a new Harness image. Existing images contain the older Python generator;
updating only the host does not replace that code. Preserve compatibility and
declaration-digest checks, and start a new campaign after updating.

For the Attack Harness maintainer:

1. Make the pinned Operator source commit available on GitHub.
2. Copy `source`, `package`, and `publisher` from Operator's
   `release/contract-lock.json` into Attack Harness's `build/contract-lock.json`.
   Preserve the Harness lock's own `api_version`.
3. Use the existing image build workflow to materialize that exact package and
   rebuild both platform images. No Harness dispatcher source change is required;
   the corrected Python projection is shipped in the shared package.
4. Select the rebuilt image and matching Operator candidate, then start a new
   campaign. Do not resume the previously failed campaign or relax startup pins.

These live calls validate direct OpenAI schema acceptance and argument generation.
They do not qualify a Docker image, a complete live campaign, Azure/LiteLLM
endpoints, or every possible model-generated argument. Local tests enforce the
complete runtime rules regardless of provider behavior. OpenAI's
[function-calling guide](https://developers.openai.com/api/docs/guides/function-calling)
describes the distinction between non-strict generation and application execution.
