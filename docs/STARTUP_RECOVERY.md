# Startup container reconciliation

`internal/startup.Acquire` returns an installation-wide worker lease only after
prior campaign execution has been reconciled. The new worker MUST retain this
lease through finalization. Acquisition neither resumes an old campaign nor opens
its journal for writing.

For each private campaign group:

1. Inspect the journal without waiting for an active writer. An active writer
   blocks acquisition, including one created by a caller without the host lease.
2. Read the saved exact Docker binding independently of journal health.
3. A complete journal without a launch start intent and without a Docker binding
   establishes that this preparation never reached container creation. Missing
   identity after a start intent, or damaged evidence without identity, blocks
   acquisition.
4. Query the saved local Docker endpoint and verify its daemon ID. List all
   container states using the full saved container ID, with nontruncated IDs and
   a fixed output format. An empty successful result followed by a matching
   daemon check establishes current absence. A failed inspect/list, abbreviated
   or unexpected ID, malformed reply, daemon change or timeout is uncertainty.
5. For a present container, verify the full ID, image, required labels and restart
   policy. An active orphan is terminated through the existing independent
   termination service and its emergency records. A live campaign writer is
   never treated as an orphan.
6. Remove an independently confirmed inactive container without force or volume
   deletion, then verify exact-ID absence again. Unconfirmed stop/removal blocks
   the new worker.

Returned per-campaign records distinguish journal integrity, never-created state,
current container absence and any new termination receipt. A damaged journal with
a valid independent binding can still have its container stopped; its bytes are
preserved and its integrity remains false. The caller MUST retain these recovery
results with a subsequently accepted launch. Neither current absence nor a
successful kill establishes historical native outcomes or complete evidence.

The read-only `dockercontrol.CheckInactive` probe does not change administrative
termination semantics: a failed inspect still cannot be reported as a confirmed
kill. It also does not repair missing start identity or silently adopt a different
container. Explicit recovery of an unknown create requires separate identity
reconciliation before the startup gate can admit another worker.

This gate handles container execution. Recovery of transient mount directories,
native target finalization, report publication and CLI/service composition remain
separate lifecycle work; none can resume failed execution.

Tests use real campaign files/locks and scripted Docker replies. They cover live
writers, orphan termination, previously removed containers, damaged evidence,
unknown create identity, daemon mismatch, failed kill/removal, invalid directory
entries and lease release on failure. Live Docker recovery qualification remains
part of runtime validation.
