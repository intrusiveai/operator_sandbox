# Operator Sandbox — container design

Implementation specification, 2026-09-19. No runtime is implemented here.

The [shared validation foundation](contracts/README.md) now provides Go/Python
strict JSON decoders, offline validation of the current schema catalog and shared
conformance vectors. The [ordinary wire contract](schemas/ORDINARY_WIRE_CONTRACT.md)
adds typed exchanges and stateless validation for 12 operations, plus spool ACKs. The [assessment contract](schemas/ASSESSMENT_CONTRACT.md)
adds typed records, structured conclusions and completion-chain checks.
Run `make setup` then `make test`. Contract publication and
Operator/Attack Harness runtime implementation remain pending.

Operator accepts structured objectives and optional scenarios from users or external
generators, runs a custom Python harness in a network-disabled container, brokers
its permitted operations, and retains host journals, evidence and local reports.

- [Objectives and scenarios submission](schemas/SCENARIO_BUNDLE_CONTRACT.md): public
  input fields, validation, immutable staging and user-authored examples.
- [Public capability export](schemas/CAPABILITY_EXPORT_CONTRACT.md): copyable typed
  references, compatibility-based admission, optional exact pins and tested fixtures.
- [Accepted shared host/harness contract](schemas/SHARED_CONTRACT.md): authoritative wire, startup,
  manifest and completion rules; package publication and full conformance remain pending.
- [Product specification](OPERATOR_SANDBOX_SPEC.md): requirements, interfaces,
  lifecycle, journaling and acceptance criteria.
- [Container guest contract](GUEST_CONTAINER_SPEC.md): proposed Python image,
  filesystem, Linux FIFO/macOS spool transport and startup restrictions.
- [Attack Harness specification](../attack_harness/GUEST_ARTIFACT_LAYOUT_SPEC.md):
  companion Python harness and container image design.

Interceptor provides the
[local MVP integration contract](../interceptor_sandbox/docs/local-api.md);
Operator runtime implementation follows the shared-contract foundation.

## Docker MVP deployment

Administrators install the matching Linux Attack Harness image in local Docker Engine (Linux)
or Docker Desktop (macOS) and configure
Operator's `engine.image`. Operator resolves the full local Docker image ID and
checks/caches the
[HTTPS release compatibility record](schemas/ENGINE_RELEASE_CONTRACT.md) before launch.
Linux hosts use private named FIFOs; macOS hosts use regular-file spools for
ordinary/control traffic. Input/skill/manifests stay read-only. Host support targets
x86_64 and ARM64/AArch64 on both OSes. Journaling remains mandatory; administrative
termination calls Docker directly without campaign-worker cooperation. Runtime
qualification is pending.

See [host runtime profiles](HOST_RUNTIME_PROFILES.md) for the support matrix and remaining decisions.

The MVP uses Docker mounts/permissions and two-stage seccomp. Additional filesystem
policy and separate user-namespace remapping are optional. Confirmed supported
OS/Docker versions are informational; startup checks required runtime capabilities.

Retained injections can be explicitly removed through the
[typed cleanup contract](schemas/INJECTION_CLEANUP_CONTRACT.md), including after a
healthy restore, without rolling back the target. Runtime implementation is pending.
