<h1><img src="assets/operator-sandbox.png" alt="Operator Sandbox icon" width="48" height="48" align="absmiddle"> Operator Sandbox</h1>

Operator Sandbox is an open-source platform for running security-testing campaigns
against AI applications. You provide objectives and optional test scenarios;
Operator runs an AI-powered [Attack Harness](https://github.com/intrusiveai/attack_harness)
that develops experiments, submits them to the target, and uses the results to
choose what to try next.

Operator manages the campaign: it controls access to the target and model provider,
enforces limits, and keeps journals, evidence, and reports for review. It supports
prompt-injection testing through [Interceptor Sandbox](https://github.com/intrusiveai/interceptor_sandbox)
and testing HTTP-accessible applications through a configured HTTPS adapter.

## How it works

1. **Connect a target.** Use Interceptor Sandbox on the same host, or configure an
   HTTPS target. A target profile defines the permitted interactions, and a
   capability export describes what the target supports.
2. **Describe the campaign.** Submit a JSON bundle containing objectives, optional
   scenarios, and any supporting artifacts. You can write bundles yourself or
   generate them with an external tool.
3. **Run experiments.** Operator starts Attack Harness in a network-disabled
   Docker container. The harness requests model calls and target operations
   through Operator's defined tools. Operator applies the configured limits and
   feedback permissions before returning results.
4. **Review the evidence.** Operator records activity and retains campaign results.
   Reports distinguish recorded observations, harness conclusions, and evidence
   gaps so you can assess what the tests actually demonstrated.

With Interceptor, the harness can create, list, and restore target snapshots to
compare experiments while retaining its campaign context. Custom skills can add
campaign guidance. Available operations depend on the selected target adapter.

## Getting started

The intended host platforms are **Linux and macOS on x86_64 and ARM64/AArch64**.
Use Docker Engine on Linux or Docker Desktop on macOS.

**Development status:** core implementation and deterministic integration tests
are in place, and selected live model-provider tests have passed. Full native
runtime qualification and approved release publication remain pending. See
[implementation status](docs/IMPLEMENTATION_STATUS.md) and
[live qualification results](docs/LIVE_QUALIFICATION_VALIDATION.md) for details.

### 1. Set up Operator

Follow the [installation guide](docs/HOST_DISTRIBUTION.md) to install `operatorctl`
and its matching shared-contract package. For source builds and checks, see
[Development](#development) below.

Prepare the following configuration:

- **Harness image:** install a matching Attack Harness image in local Docker and
  select it with `engine.image`. Operator uses the local image and validates its
  release compatibility record before launch; it does not pull images for you.
- **Model access:** choose a [model-provider profile](docs/MODEL_PROVIDERS.md) and
  configure [host authentication](docs/HOST_AUTHENTICATION.md). Credentials stay
  on the host.
- **Target:** configure [Interceptor integration](docs/CAMPAIGN_PREPARATION.md) or
  a [declarative HTTPS target](docs/HTTPS_TARGETS.md).

The installation creates a private configuration file. Its default location is
`/etc/operator/config.yaml` on Linux and
`~/Library/Application Support/Operator/config/config.yaml` on macOS. See the
[configuration guide](docs/HOST_CONFIGURATION.md) and
[example configuration](examples/operator-config.yaml) for available settings.
The example is a starting point; campaign execution also needs the installed
contract and model/target profile selections.

Check the configured installation:

```sh
operatorctl config check
operatorctl doctor
```

Use `--config /absolute/path/config.yaml` to select another configuration file.
`doctor` checks installation prerequisites; it does not run a campaign or test
live model credentials.

### 2. Prepare a campaign

Create an Operator environment directory containing `target-profile.json`,
`capabilities.json`, and the capability export's native companion file. The
[submission guide](docs/SUBMISSION.md#administrator-target-selectors-and-capability-export)
explains how to export capabilities and assemble this directory. It is separate
from an Interceptor environment blueprint.

Write `scenario-bundle.json` using the
[objectives and scenarios format](schemas/SCENARIO_BUNDLE_CONTRACT.md). Start from
an [objectives-only example](schemas/fixtures/scenario-bundle-objectives-only.json)
or a [scenario-guided example](schemas/fixtures/scenario-bundle-scenarios.json),
then replace the example target and capability references with your own export.

### 3. Run and inspect

With the configuration, target, and inputs prepared:

```sh
mkdir -p runs
operatorctl run --bundle ./scenario-bundle.json \
  --environment ./environment --output ./runs/example
```

This validates the submission and runs the campaign in the foreground. Add
`--artifacts ./bundle-artifacts` if the bundle references supporting files.
Use a new output directory for a new submission. Optional `--service` execution
is described in [administrator workflows](docs/ADMIN_WORKFLOWS.md).

Inspect the campaign, generate a report, or export its retained evidence:

```sh
operatorctl inspect --run ./runs/example
operatorctl report --run ./runs/example
mkdir -p exports
operatorctl export --run ./runs/example --output ./exports/example
```

Inspection is read-only. Generate reports and exports after execution closes.
To stop a campaign from another terminal, use its ID from the run receipt or
inspection output:

```sh
operatorctl campaign terminate --campaign CAMPAIGN_ID
```

Interrupted campaigns do not resume automatically. Retained evidence remains
available for reporting and administrative cleanup.

## Learn more

- [Campaign start and lifecycle](docs/CAMPAIGN_START.md)
- [Custom skills](docs/SKILLS.md)
- [Reports and evidence exports](docs/REPORTING.md)
- [Retained-data cleanup](docs/PURGE.md)
- [Host runtime requirements](HOST_RUNTIME_PROFILES.md)
- [Product specification](OPERATOR_SANDBOX_SPEC.md) and
  [shared Operator–Attack Harness contract](schemas/SHARED_CONTRACT.md)

## Development

Use Go 1.26.5 and Python 3.12 or later. From this repository:

```sh
make setup
make test
go build -o build/operatorctl ./cmd/operatorctl
```

This builds a development CLI. A runnable campaign installation additionally
requires the matching contract package, harness image, release compatibility
metadata, and host configuration described above. See
[release tooling](docs/HOST_DISTRIBUTION.md#reproducible-candidates-and-publication)
for reproducible installation candidates.

## License

Operator Sandbox is licensed under the [GNU AGPL 3.0](LICENSE).
