# Candidate releases

## Normal workflow

1. Create `candidate-v1.2.3`. Push changes directly or merge feature PRs into it.
   Every push runs Operator's tests and reproducible builds for Linux/macOS on
   AMD64/ARM64. After success, Actions signs the packages and publishes or replaces
   the GitHub **Pre-release** named **Operator Sandbox 1.2.3**, at tag `v1.2.3`.
2. Wait for the candidate push's **publish** job to succeed. Open a PR from
   `candidate-v1.2.3` to `main`. Keep the candidate up to date with `main` before
   merging so that merging does not change the candidate's files.
3. Merge the PR. Its closed/merged event promotes that same release to stable.
   The uploaded files, signatures and candidate tag stay unchanged. Ordinary main
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
[host distribution](HOST_DISTRIBUTION.md).

`candidate.json` and its detached signature bind the four packages to their version,
source commit and shared contract. Release metadata records the complete asset
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
