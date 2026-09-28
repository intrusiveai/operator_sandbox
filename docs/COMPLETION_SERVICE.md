# Assessment and completion service

`internal/campaignservice` implements the final two ordinary routes:
`engine.record_append` and `engine.request_stop`. The shared wire schemas and
Go/Python completion-chain validator remain the contract authority.

## Records and reference resolution

- Records MUST remain explicitly attributed harness assertions. They MUST NOT
  overwrite host execution facts, usage or final reporting status.
- The service MUST validate objective/scenario and optional surface membership,
  known hypotheses, acyclic hypothesis ancestry, prior record receipts, committed
  attempt receipts, observation visibility and ranges, and input entry ranges.
  These lookups MUST use the campaign's retained records, not guest declarations.
- Supported assessments and explicit byte ranges MUST resolve available verified
  feedback bytes. Range-free references describing unavailable feedback MUST
  still resolve visible retained entry metadata. Source assurances remain
  retained facts; assessment confidence and interpretation remain assertions.
- Records MUST retain exact request/response bytes before publishing their receipt.
  Exact duplicate operations MUST retain their original receipt and attribution.
  Same-campaign references MUST remain accessible across target restores.
- Progress records MUST be limited to one new accepted record per second.
  Replaying an accepted record MUST NOT charge the limit again or extend execution.

## Conclusion and stop

- A conclusion record MUST resolve a committed conclusion artifact, validate its
  structure and references, and match exact launch input digests, current target
  revision, image/release/package pins and finish reason.
- A committed-conclusion stop MUST validate the retained artifact-commit response,
  conclusion-record exchange and stop request together. The host MUST use its own
  expected binding and receipt lookups. Guest messages that agree with each other
  are insufficient. An explicit unavailable conclusion MUST remain incomplete.
- The host MUST durably save stop acceptance and the operation result before
  returning the acknowledgement. Acceptance MUST close all execution admission.
  During the exit allowance, exact stop duplicates MUST return the original ACK;
  changed content MUST conflict, and new execution MUST be denied.
- The ACK MUST describe finalization as pending. It MUST NOT claim that Docker
  exit, native cleanup, evidence export or reporting has completed.
- The host MUST enforce the five-second exit allowance through the independent
  Docker termination observer. Neither guest acknowledgement nor worker cleanup
  may gate termination. Administrative stop and the original hard campaign
  deadline MUST override this allowance.

## Bounded finalization

`BeginFinalization` is a host controller operation. The first conclusion artifact
begin, conclusion record or stop request MUST also enter finalization, without
an additional guest RPC. A model/tool admission limit enters the same phase.
It MUST run once, after serialized work drains, and
permit only conclusion uploads, conclusion records and stop. It MUST NOT reopen
execution or extend the campaign deadline.

The phase MUST last at most 30 seconds inside the original campaign deadline and
admit at most 16 new requests. Exact duplicates MUST NOT count again. Conclusion
part traffic MUST remain within 2 MiB in total and within existing artifact
reservations. The first conclusion slot and up to 1 MiB of artifact capacity are
protected by the artifact service. The ledger MUST allow this bounded conclusion
traffic after the exploration submission allowance is exhausted. Exceeding a
finalization bound MUST terminate execution.

The harness still owns its local model-response batch, invalid-call, no-progress
and input-read loop state under the shared harness execution rules. This host
service does not infer those guest-local events from ordinary messages.

## Validation and integration status

Tests exercise conclusion upload/record/stop through a physical file spool after
restore; original attribution on replay; unknown and out-of-range references;
record rate limits; cyclic hypotheses; mismatched conclusion binding; explicit
unavailable output; bounded finalization and independent termination. They use
real journals and shared validators with scripted native/provider/Docker peers.

All 13 ordinary routes have service handlers. Campaign launch, provider/credential
binding, reporting and Attack Harness execution are implemented. Full runtime
qualification remains outstanding. A stop acknowledgement alone establishes neither
completed finalization nor a successful report.

Native evidence verification and finalization are now implemented in the
[evidence service](EVIDENCE_SERVICE.md), independently of stop acceptance.
