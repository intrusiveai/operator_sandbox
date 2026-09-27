# Campaign preparation contract

## Administrator target profile

Installation YAML SHOULD select `target.profile_file` as an absolute path. Campaign
preparation MUST require a selected profile. The file MUST be a private regular
file owned by the service user or root; links, shared permissions and unstable
reads MUST fail. `hostconfig.ReadPrivate` supplies the same capture rules as the
installation configuration loader.

The initial profile is closed JSON:

```json
{
  "api_version": "operator.dev/target-profile/v1alpha1",
  "id": "local-interceptor",
  "target_id": "delivery-example",
  "adapter": "interceptor/v1",
  "allow_target_stop": false,
  "scopes": {
    "operation_ids": ["invoke"],
    "caller_principal_ids": [],
    "routes": [],
    "allow_retained_injections": false
  },
  "feedback": {
    "ceiling": "black-box",
    "allowed_kinds": ["target_output"],
    "max_attempt_bytes": 1048576
  },
  "operation_timeout_ms": 30000
}
```

Routes use the existing concrete `attemptadapter.Route` structure. Preparation
MUST resolve every selected operation and route against verified native capability
facts. Publication of a capability MUST NOT grant permission to use it. The profile
MUST NOT be read from the bundle, harness filesystem or working-directory defaults.

## Frozen preparation

`preparation.Build` MUST use the catalog from the verified installed protocol,
bind attachment metadata to a ready status and pinned native instance, check bundle
compatibility, resolve profile policy and verify actual artifact bytes. The initial
artifact capture is bounded to 64 MiB; later upload admission remains separate.

`Target.Context` MUST fill target, effective feedback and contract pins from those
verified inputs. Prompt, model, skill, release and resource settings MUST come from
their host preparation steps. Context projection does not grant release approval.

`Target.Persist` MUST match the immutable RunManifest's target, context, bundle,
host policy, model profile, package, release and remaining-limit pins. It MUST retain source,
policy, compatibility, bundle and context bytes plus campaign-local artifacts
before publishing preparation adoption. A failed write MUST prevent service use.
Broker artifact reads MUST use verified retained journal content, not the original
source paths or caller-owned buffers.

The [campaign service](CAMPAIGN_SERVICE.md) supplies concrete artifact, policy,
lineage and current native revision callbacks from this adopted preparation.
Preparation MUST NOT accept an unverified installed package or grant permissions
from capability publication alone.

Before launch/admission, the service MUST recheck native identity/readiness and the
exact Docker binding. Native `session.status` supplies the mutation revision for
the next command; this counter MUST NOT be confused with campaign run_revision.
Healthy restores MUST preserve original receipt sources and cumulative accounting.

Tests MUST cover unverified packages, changed process/capability bindings, foreign
or corrupt artifact bytes, mutable caller inputs, private profile loading and
manifest/context mismatches. Full launch still requires release approval, staged
input verification, the startup transcript and runtime qualification.

## Passive submitted references

Live preparation MUST revalidate the exact submitted supporting-reference inventory
and retain independent copies. Only descriptors with `operator-engine` visibility
are admitted by ScenarioBundle. Files MUST match declared size/raw digest and any
installed schema. Missing, extra, corrupt or explicitly omitted-but-supplied files
MUST fail preparation. Multiple artifact IDs may share a single digest/path only
when their size, media type and schema agree.

EngineContext reference entries MUST use canonical digest-derived paths and stable
reference IDs. Artifact bindings and explicit omissions MUST be sorted by artifact
ID; reference inventory MUST be sorted by path. Caller-supplied template references
MUST NOT override the inventory prepared by the host. Immutable staging MUST use
only copied inventoried bytes. The campaign journal MUST retain those bytes and
descriptors as `campaign.reference-staged` records for later reporting.

Passive references MUST NOT become native execution artifact receipts. The harness
MUST explicitly publish data through the ordinary artifact protocol before using it
in an experiment. Snapshot restoration MUST preserve the same reference inventory.
