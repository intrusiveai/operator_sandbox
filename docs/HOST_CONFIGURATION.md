# Administrator configuration

Status: implemented in `internal/hostconfig` and `cmd/operatorctl`. This stage
loads installation settings and makes them available to administrative termination.
The private target profile and campaign preparation library are also implemented;
campaign start, model configuration and service installation remain integration
work. The host configuration is separate from the shared
Operator/Attack Harness wire package.

## File and supported settings

The default file is `/etc/operator/config.yaml` on Linux and
`~/Library/Application Support/Operator/config/config.yaml` on macOS, with the home
directory resolved for the service account. Commands accept an absolute `--config`
path. No configuration is discovered in the working directory or campaign inputs.

The initial loader accepts exactly these fields:

| Field | Meaning and default |
| --- | --- |
| `engine.image` | Required local image selector: name/tag, repository reference with full SHA-256 digest, or full image ID. |
| `target.profile_file` | Optional clean absolute path to the private administrator TargetProfile; required for campaign preparation. See [campaign preparation](CAMPAIGN_PREPARATION.md). |
| `docker.endpoint` | Canonical local `unix:///` socket. Linux default: `unix:///var/run/docker.sock`; macOS default: `unix://<home>/.docker/run/docker.sock`. Configure rootless/custom socket locations explicitly. |
| `docker.executable` | Optional absolute Docker CLI path; omitted means resolve from the administrator/service `PATH` when constructing the Docker client. |
| `state.root` | Linux default: `/var/lib/operator`; macOS default: `<home>/Library/Application Support/Operator/data`. |
| `cache.release_directory` | Default: `<effective state.root>/cache/releases`. Must be outside `<state.root>/campaigns`, whose contents are purgeable. |
| `spool.max_bytes` | Positive decimal byte count, default `536870912` (512 MiB), maximum `9007199254740991`. Applies to macOS spool accounting; Linux uses FIFOs. |
| `evidence.max_archive_bytes` | Per-session native archive acceptance ceiling, default `4294967296` (4 GiB), maximum `9007199254740991`; positive decimal integer. Collection uses the smaller of this and Interceptor's advertised limit. |

The macOS default selects Docker Desktop's documented
[per-user socket](https://docs.docker.com/desktop/setup/install/mac-permission-requirements/#installing-symlinks)
directly, without depending on the optional `/var/run/docker.sock` symlink.

Start with the [example configuration](../examples/operator-config.yaml), choose an
already installed image, and install the file with mode `0600` in the private
configuration directory. The file must be owned by the executing service user or
root, have no group/other permissions, be regular and singly linked, and fit within
64 KiB. Symlinks, hard links and special files are rejected. Installation and runtime
directory ownership/permissions remain the administrator/launcher responsibility.
Path checks here are lexical; launch must also verify the actual filesystem layout
so symlinks or filesystem name aliases cannot place the cache in purgeable storage.

The document must be UTF-8 YAML containing one mapping of sections to field mappings.
Unknown and duplicate fields, additional documents, anchors, aliases, merge keys,
nulls and type coercions are errors. Strings must be nonempty; byte limits use
unquoted positive decimal integers. Paths must be absolute and clean, with no control
characters. Values are literal: no tilde, environment or command expansion occurs.
Parser errors never echo configuration contents. Successful loading returns the
effective settings plus a SHA-256 digest of the exact file bytes for provenance.

These settings are trusted host input. Neither Docker environment/context selection
nor campaign content overrides the endpoint or image. The release origin and TLS
policy remain fixed by the [release contract](../schemas/ENGINE_RELEASE_CONTRACT.md).
Additional policy settings described in the product specification must be added to
the typed loader with their implementation; they are not silently ignored today.

## Read-only validation

```sh
go build -o build/operatorctl ./cmd/operatorctl
build/operatorctl config check
# Or select another installed file:
build/operatorctl config check --config /absolute/path/config.yaml
```

Success emits `operator.dev/config-check/v1alpha1` JSON with `status: valid`,
`source`, `source_digest` and effective `settings`. Exit status is **0** on success,
**2** for invalid arguments or failed configuration loading, and **1** if output
cannot be written. A missing file is an error.

Validation reads the selected configuration only, checking the target profile path
without opening that file. It does not create directories,
resolve the Docker executable, contact Docker or HTTPS, or assert that images and
runtime capabilities are available. Actual preparation must perform the existing
[local image and release checks](IMAGE_PREPARATION.md). Launch must freeze the
effective settings, retain their provenance and pass the configured spool limit
to the transport without rereading configuration on target restore.

## Administrative termination precedence

`operatorctl campaign terminate` uses the loaded `state.root` and
`docker.executable`, with these rules:

1. An explicit `--config` must load successfully.
2. Explicit `--state-root` and `--docker-bin` override loaded values.
3. If `--state-root` is supplied without `--config`, skip default configuration
   entirely. This is the emergency path for damaged configuration; specify
   `--docker-bin` too if Docker is not available on `PATH`.
4. Otherwise load the default file. Only its absence permits termination to use
   the OS state-root default and Docker from `PATH`. An existing invalid/unreadable
   file fails with recovery instructions.

Termination always uses the campaign's **saved** local endpoint, daemon identity
and full container ID. The configuration's endpoint is for preparing new campaigns;
changing it cannot redirect cleanup of an existing campaign.

## Validation scope

Tests cover both OS defaults, effective overrides, strict YAML and field validation,
private file loading, file/link/type rejection, exact-byte provenance, read-only CLI
checks, missing versus corrupt configuration and recovery overrides. CLI termination
tests exercise the real subprocess wrapper with a fake Docker executable that rejects
any endpoint other than the saved campaign binding. Host/Docker qualification and
launch integration remain separate gates.
