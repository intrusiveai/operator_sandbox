# Shared contract package build

`operatorctl contract build` creates a reproducible content-only snapshot from an
explicit trusted Operator source checkout. It MUST NOT execute packaged code,
download dependencies or contact a target. Build the CLI from that same checkout;
its embedded schemas MUST match the selected source schemas.

```sh
operatorctl contract build --source /absolute/operator_sandbox \
  --output /absolute/new-contract-package --package-version 0.0.0
```

The output MUST be a new directory. The receipt contains the exact package version
and canonical SHA-256 identity. Existing output MUST NOT be overwritten. Files and
directories MUST be synchronized before `package.json` is published last; incomplete
output MUST fail the installed loader's validation.

The package MUST contain:

- Root catalog, operation registry and their complete offline schema resources.
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

```sh
operatorctl contract check --package-dir /absolute/new-contract-package \
  --package-version 0.0.0 --package-digest sha256:<approved-package-digest>
```

Operator's private `contract.directory`, `contract.version` and `contract.digest`
configuration MUST pin the installed package. The Attack Harness image MUST use
the same package identity and matching Python validator implementation.

Building or checking a package verifies content integrity. Release `0.1.0`
qualification still requires the remaining native model codecs and complete
host/harness conformance tests; a successful build alone does not qualify a runtime
or a provider route. Use `0.0.0` for the current development snapshots.
