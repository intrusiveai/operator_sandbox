# Composed launch session

`internal/hostrun` composes the verified host builders into a single-use worker
lifetime. The executable frontend supplies frozen installed profiles, the approved
image/embedded files, an attached target, native/provider clients and a successfully
acquired startup gate. These are trusted host dependencies, not guest fields.

`Prepare` MUST claim the gate once for the same state root. A failed claim does
not release another session's lease. After a successful claim, preparation owns
the lease even if a subsequent step fails.

Preparation performs these steps before returning an accepted receipt:

1. Build the complete launch inputs and effective budgets from verified inputs.
2. Create the fresh campaign journal and freeze its filesystem free-space floor
   before retaining any other event.
3. Stage immutable input, skill and manifest files; journal every mounted byte and
   the complete retention marker; persist target/capability preparation.
4. Retain per-campaign startup reconciliation results.
5. Create a fresh OS-selected transport below the campaign's runtime directory.
6. Construct the Docker launch plan and host worker without creating a container.
7. Commit `campaign.start-accepted` with the campaign, launch, start-request and
   RunManifest identities; only then return the bounded receipt.

The accepted receipt establishes prepared ownership, not successful bootstrap or
experiment completion. The frontend MUST keep the session alive and call exactly
one of `Run` or `Cancel`. Frontend start-key deduplication and background service
ownership remain separate requirements; this library never reconstructs a session
from an earlier receipt or journal.

`Run` owns the existing worker through bootstrap, execution and finalization,
then closes the writer and releases the startup lease. Unknown container outcomes
retain their mounts according to the worker's result. The frontend MUST keep its
native/model clients open until Run has returned.

`Cancel` applies only before Run; it removes never-started input/transport/policy
resources, records a terminal result, closes the writer and releases the lease.
Both methods consume the session. Neither permits a second launch. Worker failures
before Docker creation also attempt a terminal journal record, making those
outcomes observable by status/wait. Required journal failure remains an error and
cannot be reported as a successfully recorded terminal outcome.

Administrator configuration supplies journal budgets: 8 GiB cumulative bytes,
16 MiB segments and a 256 MiB filesystem free-space floor by default. Preparation
freezes the selected policy; restore does not reset it. The free-space floor is a
preflight threshold, not reserved storage. All configured counters use the shared
JSON-safe integer ceiling.

The integration tests combine real verified launch builders, retained bytes,
journals, spool files and lifecycle handling with scripted Docker/native/model
peers. They exercise a real message handshake, event-loss termination, cleanup,
prelaunch cancellation, invalid configuration, single-use ownership and lease
release. They do not qualify a deployed Python image or live model route.
