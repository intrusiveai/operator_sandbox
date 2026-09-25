# Implementation phase handoff

## Completed phase: native Interceptor client foundation

The host-only `internal/interceptor` layer now covers the current local API routes
and their transport/lifecycle envelopes. It is ready for the next host adapter and
campaign-broker phase. It does not itself authorize guest requests or run campaigns.

| Area | Implemented |
| --- | --- |
| Connection policy | Fixed IPv4 loopback endpoint; bounded JSON; no proxy, credentials, redirects, compression, connection reuse or automatic replay. |
| Target binding | Campaign attachment, active/retained session status, instance identity checks and native closure acknowledgements. |
| Native operations | Frozen v1alpha2 envelopes, exact body digests/deadlines, dispatch and operation-record reconciliation. |
| Lifecycle | Frozen restore/stop requests, replacement lineage checks and separate lifecycle-record reconciliation. |
| Snapshots | Explicit remaining byte allowance, creation receipts, native checkpoint hashes, bounded inventory and scoped inspection. |
| Evidence transport | Configurable local ceiling, pinned native ceiling, bounded streaming, size/digest checks and private temporary-file cleanup. |
| Archive structure | Native member-name restrictions, regular-file/entry/expanded-byte limits, framing/footer checks, blob hashes and read-only member access without extraction. |

The [client guide](INTERCEPTOR_CLIENT.md) records method contracts, limits and caller
responsibilities. Existing foundations also provide shared Go/Python contract
validation, host campaign persistence/terminal fencing, transport, Docker identity
and termination, local image/release validation, configuration and input staging.

## Validation achieved

- Copied native request/capability fixtures and independently generated native
  checkpoint fixtures pin relevant serialization behavior.
- Unit tests cover malformed frames, quotas, hashes, identities, failure states,
  deadlines, temporary storage and archive boundaries.
- A stateful HTTP test covers attach/status, snapshot create/list/inspect, restore
  with a lost reply, both record ledgers, cleanup closure, confirmed stop and retained
  source-session evidence download/inspection. It verifies that effects run once
  and that reads/cleanup address the correct old or replacement session.
- The lost-reply test never resumes target experiments after uncertainty. A late
  successful record is used to locate the replacement for cleanup/reporting only.

These are protocol tests against test servers. They do not qualify a real Docker
launcher, native Linux/macOS runtime, live Interceptor target or Attack Harness.
Cross-compilation establishes build compatibility only.

## Next major phase: host interpretation and campaign broker

The next layer must implement and integrate the following before an executable
campaign service can treat these client results as admitted work or final evidence:

1. Verify native capability manifests and delivery contracts; project the public
   capability export; validate objective/scenario bundle dependencies and live
   target compatibility.
2. Translate typed attempts, artifact/injection operations and feedback profiles;
   resolve handles and filter observations before exposing results to the harness.
3. Verify native evidence JSON identities, manifests, event/state hash chains,
   execution records, references, restore provenance and completeness markers.
   Structural archive inspection alone does not permit evidence publication.
4. Connect dispatch and reconciliation to durable admission, cumulative accounting,
   instance/session pins, terminal fencing and exactly-once binding/receipt adoption.
5. Publish verified per-session evidence into campaign retention, support later
   administrative import and cleanup of abandoned staging, and derive report inputs.

Operator campaign start/service orchestration, guest Docker launch/bootstrap and
runtime qualification follow integration of those host boundaries. The Attack
Harness dispatcher/model loop and its runtime tests remain a subsequent component
phase. No tool in the current client foundation bypasses these pending gates.
