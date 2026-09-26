# Campaign artifact service

The ordinary campaign service implements `engine.artifact_begin`,
`engine.artifact_put_part` and `engine.artifact_commit`. It uses the published
wire schemas; upload IDs and receipts are opaque campaign-local handles.

## Storage and admission

- The host MUST validate the declaration before allocating an upload. It MUST
  reserve one artifact object and the declared byte size from the campaign's
  remaining allowance, including artifacts retained during preparation.
- Each accepted upload MUST consume its own reservation. Incomplete uploads MUST
  retain their reservations until shutdown. No ordinary operation refunds them.
  An exact duplicate begin MUST return its saved handle without charging again.
- The host MUST protect one conclusion slot and up to 1 MiB of the initially
  available artifact allowance. Ordinary uploads MUST NOT consume this reserve.
  The first conclusion upload uses this allowance; it does not add budget.
- Parts MUST be contiguous, at most 256 KiB decoded, and fit the original size.
  New operations with gaps, overlap, or writes after commitment MUST be denied.
  Exact duplicates MUST return their saved result without appending bytes again.
- Bytes MUST be retained as private immutable journal content. Upload indexes
  MUST keep verified content descriptors rather than accumulating object bytes
  in memory. Large tool bodies MUST also use content descriptors so ordinary
  messages do not exceed the journal metadata ceiling.
- Commit MUST verify completeness, raw digest and declared canonical form against
  the original saved declaration. Integrity failure MUST terminate execution.
  A receipt MUST be durably published before the successful reply. A repeated
  commit of an already committed upload MUST return the original artifact receipt.
- Descriptor conflicts for the same digest MUST be rejected. Attempt preparation
  MUST resolve only committed campaign artifacts and recheck their retained bytes.
  The target's delivery schema and byte limits MUST still apply independently.
- Healthy restore MUST preserve uploads, committed receipts, budget reservations
  and duplicate lookup. It MUST NOT require uploading the same artifact again.
- Shutdown MUST leave artifact journal content under campaign retention policy;
  campaign purge removes it with the rest of that campaign's retained evidence.

Uploads contain no guest-selected paths. Per-part journal records preserve ordered
content references; the compact commit record adopts that upload's complete part
sequence. Storage failures fence execution; partially retained content does not
become an executable artifact. Recovery remains cleanup/reporting only.

## Validation and integration boundary

Tests cover physical spool transfer with a full-size part, duplicate IDs, offset
errors, commitment followed by native attempt execution, restore continuity,
cumulative reservations, conclusion capacity, and digest/canonical-form failures.
Large-body ledger tests check retained bytes and changed-body identity conflicts.
Native and Docker peers are scripted; these tests do not qualify a real container.

The service now supports all 13 ordinary routes. The
[completion service](COMPLETION_SERVICE.md) validates conclusion assessments and
the completion chain; successful artifact commitment alone does not certify them.
The first conclusion begin enters the bounded finalization phase.
