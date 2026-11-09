# Operator–Attack Harness contract compatibility

The compatibility identity is the version in `schemas/asyncapi.json` and SHA-256
of that exact file, including formatting and its final newline. Operator approves
this pair in `release/contract-lock.json`. Existing JSON fields `package_version`
and `package_digest` carry the interface version and document hash. They do not
identify validator source, a release archive or an Operator commit.

The self-contained document uses [AsyncAPI 3.0.0](https://www.asyncapi.com/docs/reference/specification/v3.0.0)
with explicit JSON Schema 2020-12 payloads. Consumers must support that dialect;
they must not silently treat it as draft-07. Operator's Go/Python validators
compile it offline. AsyncAPI describes the interface from Operator's perspective:
`receive` on `ordinary-out` / `control-out`, `send` on the corresponding `-in`
lanes. Ordinary requests declare their reply and `call_id` correlation. The
`x-operator-registry`, `x-operator-catalog` and `x-operator-semantics` extensions
retain typed dispatch, resource naming and stateful obligations. The document
introduces no network service or message broker.

## Authoring and approval

1. Update schemas, operation metadata and semantic rules together. Schemas and
   `schemas/asyncapi-semantics.json` are authoring inputs; the generated document
   is the published interface. Use a new contract version for interface changes.
   Update `package.package_version` in `release/contract-lock.json` accordingly.
2. Run `make generate`, then review the AsyncAPI diff. Implementation-only fixes
   need no contract change. Ordinary documentation, test and validator edits
   do not alter the interface document.
3. Approve the reviewed bytes with
   `.venv/bin/python scripts/generate_asyncapi.py --approve`. Commit the document,
   authoring inputs and lock together. CI checks them without updating approval.
4. Run `make test-contract`. This checks the document and installed resources,
   Go/Python shared fixtures, production operation routing and controlled process
   exchanges. Its report is `dist/contract-conformance.json`. It exercises direct
   input, evidence reads, corrected submissions, finish, export, FIFO/spool peers,
   admission, duplicate protection and finalization without paid model calls.
5. Run the normal affected product tests and candidate build workflow. `make test`
   already includes these host behavior tests; a second full conformance run is
   unnecessary when those checks just passed. `--document-only` checks only
   syntax, identity and resource comparison, and explicitly reports that it is
   not functional conformance.

The development AsyncAPI metaschema is vendored under `scripts/vendor/asyncapi`.
Document validation does not prove lifecycle behavior, receipt ownership, budget
enforcement or runtime reliability. Those obligations remain executable tests.

## Consuming the interface

Obtain the approved document and a tested shared validator distribution. Verify
its expected version/hash independently of the incoming payload. No particular
Operator implementation commit is a compatibility prerequisite.

Python can compile the interface directly:

```python
from operator_contracts import Protocol
protocol = Protocol.from_interface(document_bytes, {
    "package_version": approved_version,
    "package_digest": approved_sha256,
})
```

Go provides `contracts.LoadInterfaceProtocol(documentBytes, expectedIdentity)`.
Both extract the offline resources from the document, verify identity, and use
the existing validators and correlation checks. `InterfaceResources` /
`interface_resources` provide the extracted resources for implementation tests.
Host installation still uses the package loader to check its file inventory and
reject local mutation. A changed implementation distribution has a different
inventory but can retain the same interface identity.

Attack Harness must test its actual builders, decoders, model normalization,
startup and stateful dispatcher against this document and shared fixtures. It
must remove source-revision equality and whole `contracts/` / `schemas/` tree
comparisons as compatibility requirements. An optional cross-product process test
may record the tested revisions as provenance; it must not make unrelated product
builds incompatible. Full campaigns and live provider tests remain separate
qualification evidence.

## Runtime and migration

The interface approved in the [release contract lock](../release/contract-lock.json)
uses exact document identity; legacy package-manifest identities
are incompatible. Distribution manifests use `operator.dev/contract-package/v2`.
The local image compatibility file uses
`operator.dev/harness-compatibility/v1alpha2`, containing only its API version,
contract version/hash, runtime profile and native platform. The engine manifest
and existing bootstrap/context identities must agree. Operator checks these
before execution; no additional completion handshake or runtime negotiation is
introduced. Platform/runtime and image integrity checks remain required.

A matching Harness must adopt these formats and identity rules before deployment.
Saved campaigns retain their original identities and remain inspectable; they are
not re-admitted or resumed. Updating an implementation does not rewrite evidence.

## Tool-correction conformance

The shared contract defines bounded schema-rule references in diagnostics and the
`no-progress-limit` stopping condition. See [tool corrections](TOOL_CORRECTIONS.md).
Both validators consume `schemas/fixtures/tool-corrections.json`; constraint-family
checks exercise corrected values as well as rejection. The production-process
suite covers partial reads and citations, assessment repair without target replay,
no-progress exhaustion, token-budget exhaustion, and separate CLI report/export.
Local controlled responses establish correction behavior, not live model reliability.

Model conformance validates the compact relay and accounting. Provider request
construction and response interpretation are tested in product adapters; additional
provider metadata does not change the shared interface identity.
