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

## Production launch inputs

`Target.BuildLaunch` MUST consume the checked local image/release and embedded
files, private model profile, explicitly verified skill selection, administrator
limits and host-assigned launch identity. It MUST derive native tool declarations
from the installed package, compose exact prompt bytes, apply bundle budget
narrowing, and fill target/feedback/reference bindings from live preparation.

It MUST construct EngineContext, InputTreeManifest and RunManifest in that order,
with canonical object digests and separate raw file digests. It MUST validate the
model policy and complete manifest set, and return copied staging contents.
The skill-loader digest MUST match the admitted embedded implementation. Model
endpoints and credential selectors MUST remain outside guest-visible inputs.

Construction MUST NOT perform model calls, publish execution authority or fabricate
guest bootstrap replies. The worker MUST receive the returned inputs and carry out
the live bootstrap before admission. CLI/service composition MUST still claim a
fresh host campaign, persist preparation and launch inputs, recheck image/target
identity, stage files and start the worker. Tests MUST cover all five codecs,
selected validated skills, supporting references, prompt composition, complete launch
identity validation and actual immutable file staging.

## Durable launch-input retention

Before execution, the worker composition MUST call `Launch.RetainInputs` for the
actual verified staged tree. It MUST match the campaign manifest and package pins,
visit mounted files in canonical path order, and journal exact bytes in bounded
parts with path, size, raw digest and part numbering. The private staging wrapper
receipt MUST be verified separately from the mounted file count/byte inventory.
A completion record MUST be written only after a final inventory check and matching
aggregate counts. Failure MUST fence execution; retries MUST NOT append a second
retention transaction through the same launch object.

These retained inputs include the effective prompt, input/skill manifests, selected
skill files, full engine context, ScenarioBundle and supporting references. Their
journal records belong to the campaign's independently purgeable storage. Retention
MUST NOT fabricate bootstrap replies or grant execution authority; target adoption
and live bootstrap remain independent gates.

## One worker per installed state root

The worker MUST acquire `campaign.AcquireHostLease` before campaign acceptance and
hold it through execution/cleanup. All workers MUST use the same administrator-owned
state root. The lease MUST use a stable private regular lock file and nonblocking
OS locking; its file MUST NOT be unlinked on release. Unsafe links, public files
and competing workers MUST fail. Process exit MUST release the OS lock.

Acquiring this lock MUST NOT imply that containers left by a previous process have
stopped or permit execution recovery. Startup MUST reconcile persisted Docker
bindings and unresolved launch outcomes before accepting another campaign.
Administrative termination, status and evidence inspection MUST remain usable
without acquiring the execution lease.
