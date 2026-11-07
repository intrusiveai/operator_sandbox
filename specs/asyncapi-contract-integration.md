# Operator–Attack Harness interface contract

## Compatibility identity

Operator MUST publish `schemas/asyncapi.json`, a self-contained AsyncAPI 3.0.0
JSON document with explicitly declared JSON Schema 2020-12 payloads. The exact
file bytes, including whitespace and its final newline, MUST determine the
SHA-256 contract hash. Its `info.version` MUST determine the contract version.
The coordinated migration uses version 0.7.0. Older manifest-hash identities MUST
NOT be accepted as this identity.

`release/contract-lock.json` MUST contain the approved version and hash. Product
source commits, validator implementations, tests, documentation, dependency locks,
and package inventories MUST NOT contribute to compatibility identity. Build
provenance and package file integrity MUST remain independently checked.

Existing `package_version` / `package_digest` fields MUST carry this pair, with
that meaning documented explicitly. Operator MUST compare the pair in local
Harness compatibility metadata and the existing startup exchange before admitting
execution. A minimum Operator product version MUST NOT be an additional protocol
gate. Image/platform/runtime validation MUST remain in force. Retained campaign
records MUST retain their original identities; this change MUST NOT reinterpret
old records or resume their execution.

## Interface description and conformance

The document MUST describe the four existing lanes, Operator-relative send/receive
directions, ordinary request/reply correlation, startup control messages, error
responses, and the complete operation registry. It MUST include all referenced
schemas without network resolution. Extensions MUST describe FIFO/spool framing,
startup admission, ordering, duplicate protection, evidence visibility, completion,
and budget semantics that JSON Schema alone cannot establish.

Existing schema/registry source files MAY remain authoring inputs. A deterministic
generator MUST check their agreement with the committed document. Both Go and
Python package loaders MUST verify that the installed schemas and registry match
the approved document, in addition to checking the package inventory. A change to
implementation or tests alone MUST leave the contract identity unchanged.

A repeatable offline conformance command MUST verify document identity, schema
and registry agreement, actual host operation dispatch coverage, shared Go/Python
fixtures, and controlled startup/request/reply/finish exchanges. It MUST fail on
missing operations, incompatible responses, correlation failures, and altered
contract bytes. Stateful tests MUST cover duplicate delivery, inspected evidence,
finish acceptance versus cleanup, deadlines, and no replay. Conformance evidence
MUST distinguish these checks from full runtime or cloud qualification.

## Distribution and counterpart adoption

Host releases MUST include the exact document and compiled identity. Release
metadata MUST derive the contract version from those bytes. Package manifests
MUST continue binding installed files without becoming a counterpart source pin.
Attack Harness MUST consume the approved document and matching shared validators,
compare version/hash, and run its own implementation conformance tests. It MUST NOT
require a particular Operator source revision or equality of implementation trees
for compatibility. Optional process-integration tests MAY record selected product
revisions as test provenance only.

Documentation MUST explain generation, approval, installation, conformance, and
migration. Verification MUST demonstrate unchanged identity after a validator-only
edit, rejection of mismatched identities before execution, and a controlled
campaign with input, evidence inspection and a single finish request. Releases and
paid cloud tests require separate authorization.
