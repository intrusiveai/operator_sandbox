# Candidate releases

## Normal workflow

1. Create `candidate-v1.2.3`. Push changes directly or merge feature PRs into it.
   Every push runs Operator's tests and reproducible builds for Linux/macOS on
   AMD64/ARM64. After success, Actions signs the packages and publishes or replaces
   the GitHub **Pre-release** named **Operator Sandbox 1.2.3**, at tag `v1.2.3`,
   then publishes its hidden `rc` catalog entry through the releases service.
2. Wait for the candidate push's **publish** job to succeed. Open a PR from
   `candidate-v1.2.3` to `main`. Keep the candidate up to date with `main` before
   merging so that merging does not change the candidate's files.
3. Merge the PR. Its closed/merged event promotes that same release to stable.
   The uploaded packages, signatures and candidate tag stay unchanged. Catalog metadata changes from `rc` to `release`, making it eligible for the website listing. Ordinary main
   pushes run verification; they do not build replacement release packages.

The numeric package version is `1.2.3` throughout. Prerelease is a GitHub release
status, not an `-rc` suffix inside the executable. The tag points to the candidate
source commit, even if the main merge uses another commit ID. Merge, squash and
rebase merges are supported when their resulting file tree equals the candidate.
The newest semantic release is eligible for GitHub's Latest designation; publishing
an older maintenance version does not deliberately replace a newer version.

The installer rejects different contents under an already installed version. Use
fresh installation directories when testing successive builds of the same rolling
candidate. Running campaigns keep their existing installed binaries and inputs.

## One-time repository setup

Configure these under **Settings → Secrets and variables → Actions**:

| Setting | Kind | Value |
| --- | --- | --- |
| `RELEASE_SIGNING_KEY` | Secret | Armored publisher OpenPGP private signing key (or exported signing subkey). |
| `RELEASE_SIGNING_PASSPHRASE` | Secret | Its passphrase; omit for an unencrypted CI signing key. |
| `RELEASE_SIGNING_FINGERPRINT` | Variable | Full 40- or 64-character fingerprint selected for signing. |
| `RELEASE_TOKEN` | Secret, when needed below | Repository-scoped token with Contents read/write and Workflows write; Pull requests read for promotion. |

The workflow normally uses `GITHUB_TOKEN` with job-scoped permissions. GitHub can
require Workflows write when creating/moving a release tag to code that changes
workflow files relative to the default branch. `GITHUB_TOKEN` cannot receive that
permission; configure `RELEASE_TOKEN` for candidates that include those changes.
A fine-grained token restricted to this repository is sufficient; an existing
suitably scoped App installation token can also be supplied. Artifact downloads
use the current job's token with Actions read, separately from release publication.

Publish the trusted public key to administrators through your established trusted
channel. Downloading a public key alongside an archive is not independent trust.
Private keys are imported into a temporary GPG home in the signing step and removed
on exit. Passphrases go to GPG through stdin, never command arguments.

Rolling prereleases require GitHub release immutability to be **disabled** and tag
rules to permit the publisher to create/update `v*` candidate tags. Stable releases
are protected from replacement by the publisher's checks. Review branch rules so
only trusted maintainers can push candidate branches or alter release workflows;
only candidate publication and same-repository merged release PRs receive signing
or publishing credentials. Pull-request verification receives none of those secrets.
No repository settings or secrets are created automatically by this implementation.

## What is published

Each release includes four platform archives (`linux-amd64.tar.gz`,
`linux-arm64.tar.gz`, `darwin-amd64.tar.gz`, `darwin-arm64.tar.gz`), each containing the
executable, contract, configuration templates, documentation, SBOM, provenance and
signed manifest. Each has `<platform>-SHA256SUMS` and `<platform>-SHA256SUMS.asc`.
Verify those named checksum files before unpacking, as described in
[host distribution](../../docs/HOST_DISTRIBUTION.md).

`candidate.json` and its detached signature bind the four packages to their version,
source commit and shared contract. Release metadata records the complete signed-package asset
inventory and GitHub-reported SHA-256 digests for promotion checks.

Operator CI and signatures govern publication. Attack Harness reports, release
service approval and native runtime qualification are separate. Publishing a
package does not approve a harness image or claim that all native profiles passed.

## Failure and retry

- Failed tests/builds/signing do not publish a new candidate.
- An older build whose branch has advanced does not overwrite the newer candidate.
- Upload replacement temporarily makes the release a draft. Interrupted uploads
  leave it unpublished; rerun the failed candidate workflow to complete publication.
- Release updates and promotion are serialized per version. Wait for publication
  before merging. If merge occurs too early, finish candidate publication, then
  rerun the failed promotion job from the merged PR's workflow run.
- Missing assets, altered hashes, changed tag, wrong candidate head, or a different
  main merge tree block promotion. Do not bypass these by rebuilding on main.
  Resolve main/candidate differences before merging; an already incorrect merge
  needs a corrected candidate PR and matching published build.
- A stable version cannot be republished by candidate pushes. Use a new version.
- A closed but unmerged PR, a fork PR, or an ordinary main push never promotes.
- Manual **Run workflow** on a candidate branch retries build/publication using its
  current head. Direct local merges pushed to main have no merged PR identity and
  do not promote; use the candidate-to-main PR workflow.

## Verification

The local workflow tests exercise push, rolling replacement, merged-PR promotion,
exact byte preservation, duplicate promotion, malformed branch versions, stale
builds, failed uploads/retry, missing/altered assets and changed merge trees against
a simulated GitHub service. Signing tests use a real passphrase-protected temporary
GPG key. Live Actions execution/publication requires the repository settings above;
local tests do not publish releases or configure GitHub.

References: [GitHub release API](https://docs.github.com/en/rest/releases/releases),
[merged PR events](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#pull_request),
[release immutability](https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository).

## Releases service catalog

After GitHub publication, the workflow MUST attach `release-index-entry.json`,
generated from the four actual uploaded archive URLs, filenames and SHA-256 digests.
Candidate entries MUST use `tags: ["rc"]`. Candidate-to-main promotion MUST retain
those exact artifact identities and update only the catalog label to `["release"]`.
Operator's existing passing CI/signing and reviewed candidate merge are the agreed
publication gates; these labels do not assert additional native QA results.

The parent workflow MUST dispatch `publish-catalog.yml` at the published `v*` tag
and wait for the matching run to finish successfully. The tag caller MUST verify
its source and catalog asset against the actual GitHub Release, and recheck the
recorded merged candidate PR for stable entries, before calling the
reviewed reusable publisher pinned to
`fc4618b56a7862a410558e7824b3f5681e426714`. The reusable publisher validates the entry,
merges it into S3, and verifies S3/public delivery. A dispatch acknowledgement,
skipped run, failed run, or timeout MUST NOT count as publication success.

That service revision MUST be available on GitHub. The caller workflow and helper
MUST exist on both Operator's default branch and the selected tag before dispatch.
The releases repository MUST permit this repository to use its reusable workflow.

Configure the **production environment in intrusiveai/operator_sandbox** with:

| Environment variable | Purpose |
| --- | --- |
| `AWS_REGION` | Region of the existing releases service. |
| `CONTENT_BUCKET` | Existing releases content bucket. |
| `CLOUDFRONT_DISTRIBUTION_ID` | Existing releases distribution. |
| `CATALOG_PUBLISHER_ROLE_ARN` | Operator's catalog-only role from the service's `catalog_publisher_role_arns` output. |

The environment MUST permit protected `v*` tags. Retain configured environment
reviewers. GitHub OIDC supplies AWS credentials only to the reusable publisher;
Operator MUST NOT publish image approval records or another product's catalog.
No AWS access-key secrets, service-side Git writes or per-release site deployment
are needed. Initial service deployment/role setup belongs to the releases repository;
this change does not provision or alter that infrastructure.

`/docs/releases/operator_sandbox.json` retains candidates and release history. The
website displays only the first two entries labeled exactly `release`; an `rc`
entry is readable in the catalog but hidden in the website listing. The publisher
preserves other versions and refuses to demote or change a final version's artifacts.
Never upload an entire locally reconstructed catalog over the live catalog.

### Publication failures and recovery

GitHub publication can succeed while catalog publication fails. Inspect the linked
catalog run and fix its cause; do not rebuild or re-sign a stable release. If the
entry is already correct, rerun that tagged caller. If metadata upload was interrupted,
run `catalog-sync` from the reviewed checkout to recreate it from the existing assets:

```sh
GITHUB_REPOSITORY=intrusiveai/operator_sandbox \
  python3 scripts/publish_host_release.py catalog-sync \
  --tag v1.2.3 --source FULL_CANDIDATE_COMMIT_SHA
```

This uses an authenticated `gh` token with Contents and Actions write. Branch/PR
workflows already have those permissions on `GITHUB_TOKEN`. The source must match
the published tag; do not substitute the main merge commit. An already-stable release
retains its `release` label on retries. The stable promotion job can also be rerun:
it verifies the existing packages, then repairs/retries catalog publication.

Publication waits up to 30 minutes, including environment approval. A timeout
requests cancellation of a known run. Before retrying or updating the rolling tag,
confirm that any timed-out or manually dispatched older catalog job has stopped.
Do not run manual catalog repair concurrently with candidate replacement/promotion.
After success, inspect the public catalog and website: candidate remains hidden;
stable appears according to the first-two-release rule, with the exact download
links and checksums. Website pages read the live catalog without a site deployment.
