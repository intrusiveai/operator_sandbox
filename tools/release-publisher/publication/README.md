# Product-local release publisher

Commit this complete directory to the product repository as
`tools/release-publisher`. Copy `publication/examples/publish-catalog.yml`
to that repository's `.github/workflows/`. These are ordinary jobs that check out
only the product repository. There is no runtime download, action, reusable
workflow, or Go dependency from the private releases repository.

`publisher-source.json` identifies the exact source commit, product, file list,
and SHA-256 of each copied file. A `working_tree: true` export is for local tests
only. Obtain production exports from a reviewed commit with
`scripts/export-publisher.py` in an authorized developer's releases checkout;
updates use a new export and a reviewed diff in the product repository. Replace
the entire old publisher directory with the reviewed export, retaining local
integration changes explicitly rather than silently overwriting them. Do not add
a token that lets product workflows read the private releases repository.

## Dependencies and offline verification

Linux GitHub runner, Bash, Go 1.26.5 (see `go.mod`), AWS CLI v2 with conditional
`put-object` support (`--if-match` and `--if-none-match`), jq, curl, Python 3 for
tests, and standard shell tools including cmp. Go packages use only the standard
library. The module name is a local import namespace; Go does not fetch that
repository. All three products carry the same catalog code and tests. No website, Terraform, or
external Go dependency is included.

From this directory, with Go already installed:

```sh
export GOWORK=off GOPROXY=off GOTOOLCHAIN=local
go test -race ./...
go vet ./...
python3 -m unittest discover -s scripts/tests
```

Tests run actual merge/validation and publication scripts against simulated S3
and a local HTTP server. They cover candidate creation/replacement, unchanged
promotion, stale demotion/final replacement rejection, concurrent writes, bounded
retries, history/order, required shared contract metadata, and contract continuity.
They do not perform AWS writes or prove a live GitHub OIDC exchange.

## Catalog publication (all products)

The ordinary job must run in `.github/workflows/publish-catalog.yml`, on a
protected `refs/tags/v*` ref, using the product's `production` environment. Set
`AWS_REGION`, `CONTENT_BUCKET`, `CLOUDFRONT_DISTRIBUTION_ID`, and
`CATALOG_PUBLISHER_ROLE_ARN`. Its one-time custom OIDC subject template, in order,
is `repository_id`, `repository_owner_id`, `environment`, `ref`, `workflow_ref`.
Assume the configured role with audience `sts.amazonaws.com`, then:

```sh
bash scripts/publish-catalog.sh /absolute/path/release-index-entry.json operator_sandbox
# Or interceptor_sandbox / attack_harness, matching this repository.
```

Generate the entry from actual published artifacts. Operator and Attack Harness
must include `contract_package_version`, the exact `major.minor.patch` version
of the embedded shared package (no `v`, ranges, suffixes, whitespace, or leading
zeroes; maximum 128 characters). Verify that every released platform agrees with
the package/verified manifest before generating the entry. Interceptor omits it.
The website shows this version beneath each Operator and Attack Harness release.
Those products must use the same shared contract version; their product versions
may differ. This metadata does not replace product qualification.

Candidates use `["rc"]`; qualified releases use `["release"]`. Promotion of an existing version retains
all artifact URLs, filenames, platforms, and digests, or the exact image reference
and digest, plus `contract_package_version`. A rebuilt candidate may change the
contract only alongside changed artifact identities; qualify the new bytes.
New versions prepend; updates keep their position. Final entries
cannot change artifact identity or recorded contract version, or lose the release
label. The product pipeline owns qualification and continuity when promoting to a *different* version.
The catalog publisher neither downloads all assets nor runs QA.

The script checks S3 readback and public delivery. Publication failures after a
successful write may be retried with the same inputs; concurrent writes are
re-read and re-merged with bounded retries. Routine publication needs no Terraform,
site build, or deployment. The retired compatibility API and record publisher are
not included. Attack Harness uses the same catalog publication path as the other
products; candidates can be built and tested before production qualification.
