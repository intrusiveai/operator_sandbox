# Local instruction skills

Operator validates administrator-selected custom skills as instruction data and
binds their immutable manifests and files with content digests. It never executes source files,
commands, hooks, templates or package installers.

## Administrator commands

```sh
operatorctl skill build --config /absolute/config.yaml --project project-01 --source ./my-skill
operatorctl skill check --config /absolute/config.yaml --skill sha256:<manifest-digest>
operatorctl skill import --config /absolute/config.yaml --source /absolute/skill-bundle
```

The configuration MUST select a verified shared contract package for build,
import and check.
The configured state root MUST already exist. Build creates its private `skills`
child and returns the manifest digest; it does not start a campaign.

Administrators MUST control the private skill store and explicitly select skills.
The harness MUST NOT install, modify or discover additional skills.

## Source format

```markdown
---
name: example
description: Guide experiments using the supplied target context.
---
Instructions for the harness go here.
```

The frontmatter MUST contain exactly `name` and `description`. Project and name
MUST each be 1–63 ASCII characters, start with an alphanumeric character and use
only alphanumeric characters, `.`, `_` and `-`. The stable skill ID is
`<project>:<name>`. Description MUST be nonblank, contain no control characters and
fit within 1,024 Unicode characters. The Markdown body MUST be nonblank.

Optional references MUST be UTF-8 `.md`, `.txt`, `.json`, `.yaml` or `.yml` files.
Ingestion normalizes CRLF to LF and path names to NFC; standalone CR, invalid
UTF-8, NUL, invalid JSON, duplicate JSON keys and unsafe YAML MUST be rejected.
YAML MUST use only ordinary scalar/map/sequence data, string map keys, unique
keys, no anchors or aliases, at most 32 nesting levels and 16,384 nodes. No
interpolation mechanism exists. Quoted code examples remain ordinary text.

The source MUST have at most 1,024 files, 1 MiB per file, 8 MiB total content,
16 path components and 2,048 filesystem entries including directories. Files MUST
be regular, nonexecutable and singly linked, without setuid/setgid metadata.
Ingestion MUST reject symlinks, special files, cross-device entries, unsafe paths,
case/Unicode collisions and sources that change while being read. Hidden paths,
`scripts`, `hooks`, `plugins`, `agents`, `commands`, `node_modules`, `mcp`, `lsp`
directories and known package/plugin registration filenames MUST be rejected.
Extended attributes MUST be rejected, except for macOS's automatically assigned
`com.apple.provenance`. No source attributes are copied into the stored bundle or
staged mount tree.

## Immutable storage and selection

An installed bundle lives at `<state.root>/skills/<manifest-sha256-hex>/`:

```text
manifest.json
files/SKILL.md
files/<declared-reference-paths>
```

`manifest.json` MUST be canonical JSON satisfying the shared SkillManifest schema.
Its raw/canonical SHA-256 is the bundle identity. Each file's exact normalized
bytes, path, size and media type are bound by that manifest.

Import and selection MUST verify the manifest schema, exact inventory and content
policy again. Import MUST reject changed bytes rather than repair normalization.
The bundle root MUST contain exactly `manifest.json` and `files/`.
The manifest MUST be written last after file/directory synchronization. Incomplete
publication MUST fail admission; a retry checks an existing complete object and
MUST NOT overwrite it. Store files remain private; launch staging produces the
separate verified read-only mount tree.

The selection library accepts an explicit digest list and the admitted loader
implementation digest. It MUST produce a canonical SkillSetManifest with sorted
skill IDs, contiguous slots and the shared loading digest. Duplicate or colliding
skill IDs, more than 16 bundles or more than 64 MiB total content MUST fail the
whole selection. An empty list selects no skills and MUST NOT scan the store.
Selection returns frozen copies suitable for staging. Campaign preparation/start
wiring and the image's instruction loader are implemented separately.

Campaign/session purge MUST NOT delete this installation-wide skill store.

## Frozen sets and reversible removal

```sh
operatorctl skill set --skill sha256:<bundle-digest> \
  --loader-digest sha256:<approved-image-loader-digest> --output ./skill-set.json
operatorctl campaign prepare --run ./runs/example --skill-set ./skill-set.json
operatorctl campaign start --run ./runs/example
operatorctl skill remove --skill sha256:<bundle-digest>
```

`skill set` MUST validate every explicitly selected installed bundle and
publish the canonical shared SkillSetManifest to a new file. Omitting `--skill`
MUST produce the canonical empty set. The administrator MUST supply the loader
implementation digest associated with the intended image; selecting a different
image loader MUST fail before native attachment or harness launch. Publication
MUST NOT overwrite existing files. Build/import MUST remain the commands for adding
skills to the installed store.

`campaign prepare`, `campaign start` and `run` MUST accept `--skill-set FILE`,
exclusive with individual `--skill` selections. Offline preparation MUST validate
the bounded manifest, recompute its loading digest from verified installed bundles
and compare all descriptors. The file path and exact bytes MUST participate in
start identity. An omitted selection MUST reuse the saved selection; changes MUST
require explicit new-campaign selection. Online preparation MUST reverify bundles
and match the frozen loader digest to the approved image. A set file MUST NOT
permit ambient skill discovery.

`skill remove` MUST remove only the selected hash-named installed bundle tree and
return `operator.dev/skill-removal/v1alpha1` with `removed` or `already_absent`.
Removal MUST serialize against store capture/publication, use bounded no-follow
same-device traversal and preserve other bundles and campaign data.
Busy/unsafe/partial removal MUST fail with an actionable diagnostic and remain
retryable. Incomplete installed copies MUST be removable even when their manifests
or inventories cannot be validated. Removal MUST NOT require a usable contract
package.

Running campaigns MUST keep their frozen skill inputs. An administrator who wants
to stop such a campaign MUST use the existing campaign termination command.
Future selections of a missing bundle MUST fail until the administrator builds or
imports it again. The same valid digest MUST be eligible for reinstallation and
selection. Changing a future campaign's list MUST only affect that new campaign.
