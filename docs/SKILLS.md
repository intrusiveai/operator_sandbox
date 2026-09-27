# Local instruction skills

Operator validates custom skills as instruction data and signs their immutable
manifests with an installation-local Ed25519 key. It never executes source files,
commands, hooks, templates or package installers.

## Administrator commands

```sh
operatorctl skill keygen --config /absolute/config.yaml
operatorctl skill build --config /absolute/config.yaml --project project-01 --source ./my-skill
operatorctl skill check --config /absolute/config.yaml --skill sha256:<manifest-digest>
operatorctl skill import --config /absolute/config.yaml --source /absolute/signed-bundle
```

The configuration MUST select a verified shared contract package for build,
import and check. Key generation requires only the private host configuration.
The configured state root MUST already exist. Build creates its private `skills`
child and returns the manifest digest; it does not start a campaign.

Key generation MUST be explicit. It creates `skill-signing/` beside the installed
configuration file, with `private.key` (64 raw Ed25519 bytes) and `public.key`
(32 raw bytes). The directory MUST be mode 0700 and the keys mode 0600. Existing
key directories MUST NOT be overwritten. Losing a private key prevents new builds;
verification needs only the independently installed public key. Removing/replacing
that public key invalidates admission of bundles signed by the old key. It does
not retroactively terminate running campaigns.

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

## Signed storage and selection

An installed bundle lives at `<state.root>/skills/<manifest-sha256-hex>/`:

```text
manifest.json
signature.json
files/SKILL.md
files/<declared-reference-paths>
```

`manifest.json` MUST be canonical JSON satisfying the shared SkillManifest schema.
Its raw/canonical SHA-256 is the bundle identity. Each file's exact normalized
bytes, path, size and media type are bound by that manifest.

`signature.json` MUST be a closed object with `api_version` equal to
`operator.dev/skill-signature/v1alpha1`, `key_id` equal to the SHA-256 of the installed
raw public key, `manifest_digest`, and a standard padded base64 `signature`.
Ed25519 signs the canonical JSON object with `signature` omitted. The versioned
payload separates skill signatures from other signed object types.

Import and selection MUST verify the installed key, signature, manifest schema,
exact inventory and content policy again. A key supplied in a bundle MUST NOT
grant trust. Import MUST reject changed bytes rather than repair normalization.
The signature is written last after file/directory synchronization. Incomplete
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
