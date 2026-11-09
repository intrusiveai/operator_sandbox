# Shared contract package build

`operatorctl contract build` creates a reproducible content-only snapshot from an
explicit trusted Operator source checkout. It MUST NOT execute packaged code,
download dependencies or contact a target. Build the CLI from that same checkout;
its embedded schemas MUST match the selected source schemas.

Read the version from `package.package_version` in the selected checkout's
[contract lock](../release/contract-lock.json):

```sh
OPERATOR_SOURCE=/absolute/operator_sandbox
CONTRACT_VERSION=$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["package"]["package_version"])' "$OPERATOR_SOURCE/release/contract-lock.json")
operatorctl contract build --source "$OPERATOR_SOURCE" \
  --output /absolute/new-contract-package --package-version "$CONTRACT_VERSION"
```

The output MUST be a new directory. The receipt contains the exact package version
and SHA-256 of exact `asyncapi.json` bytes. Existing output MUST NOT be overwritten. Files and
directories MUST be synchronized before `package.json` is published last; incomplete
output MUST fail the installed loader's validation.

The package MUST contain:

- Root `asyncapi.json`, catalog, operation registry and complete offline schemas.
- `source/schemas/`: schemas, normative contract documents and common fixtures.
- `source/contracts/`: matching Go validator sources/tests and the Python package,
  tests and dependency lockfile under `python/`.
- `source/go.mod` and `source/go.sum`: contract-only Go dependency pins selected
  from the source checkout, independent of Operator's cloud-provider dependencies.
- Canonical `package.json`: every payload path, size and digest plus the required
  JCS, portable-manifest-path and harness-loop semantic profile bindings.

The builder MUST exclude ambient caches, virtual environments, Git metadata and
host-only implementation packages. Source reads and output inventories MUST satisfy
the shared package size/file/path limits. A source tree MUST remain stable during
publication; the publisher SHOULD build from an immutable release checkout.

## Validation before distribution

The publisher MUST run both language suites on the generated source layout before
distributing a supported release. For a prepared Python test environment:

```sh
cd /absolute/new-contract-package/source
go test ./contracts/...
PYTHONDONTWRITEBYTECODE=1 PYTHONPATH=contracts/python \
  /absolute/test-venv/bin/python -m unittest discover -s contracts/python/tests -v
```

Python dependencies MUST match `source/contracts/python/requirements.lock`.
`PYTHONDONTWRITEBYTECODE=1` prevents new cache files from violating the exact package
inventory. Test/build tooling MUST keep caches outside the package directory.

The supported release process MUST independently distribute the approved version
and digest alongside the Operator and Attack Harness builds. Runtime installation
MUST NOT derive trust merely by reading an incoming `package.json`.

Read `package.package_digest` from that same [contract lock](../release/contract-lock.json)
before running the check:

```sh
CONTRACT_DIGEST=$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["package"]["package_digest"])' "$OPERATOR_SOURCE/release/contract-lock.json")
operatorctl contract check --package-dir /absolute/new-contract-package \
  --package-version "$CONTRACT_VERSION" --package-digest "$CONTRACT_DIGEST"
```

Operator's private `contract.directory`, `contract.version` and `contract.digest`
configuration MUST pin the installed package. The Attack Harness image MUST use
the same interface identity and conforming Python validator implementation.

The [release contract lock](../release/contract-lock.json) binds only the supported
interface version and exact document hash. Product source revisions belong in
build provenance. Implementation-only changes do not require another product to
update its contract approval. Building or checking a package verifies its content integrity;
it does not by itself establish provider or container runtime behavior.

The [host distribution builder](HOST_DISTRIBUTION.md) reproduces the independently
pinned `release/contract-lock.json`, tests both languages in the generated source
layout and embeds the exact package in each host candidate. The binary and
release manifest carry the same version/digest. Publication preflight MUST verify its own packaged interface identity on each
platform; it MUST NOT require a particular Harness build or Operator source pin.
Both products run their [conformance checks](CONTRACT_CONFORMANCE.md). These tools
distribute matching candidates; they do not grant production qualification.
