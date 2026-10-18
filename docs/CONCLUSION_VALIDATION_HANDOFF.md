# Conclusion validation: Operator and Attack Harness

## Operator change

Branch `fix/conclusion-hypotheses-diagnostics` targets `candidate-v0.1.0`.
Conclusions may introduce hypotheses without earlier `engine.record_append`
hypothesis records. All local definitions are visible throughout the conclusion,
including forward references. Explicit references can also resolve through campaign
history. No history is synthesized. Duplicate IDs, self-parenting and cycles remain
invalid, along with all existing binding, receipt, visibility and evidence checks.

Operator returns an actionable `CONCLUSION_INVALID` message and the existing
`instance_path`. It retains structured rejection facts through journal, completion,
inspection and report/export. There is no new wire field or operation.

## Required Harness follow-up

1. After this Operator branch is published, use its `release/contract-lock.json`
   as the source of truth. Build shared package **0.0.6** from the exact source
   commit in that lock; compare its full package digest and inventory.
2. Update the Harness shared-package lock/build pin to that matching package.
   Rebuild both native-platform images with the same pin in their embedded
   `compatibility.json` and startup metadata. The image remains locally validated;
   no releases-service admission lookup is introduced.
3. Run the shared Python fixture suite and Harness tests. The updated Python
   validator adds order-independent local parent-cycle checks. Unknown nonlocal
   parents remain host-resolved; do not reject all historical references locally.
4. Keep the existing error envelope handling. Do not add synthetic hypothesis
   records, or require earlier hypothesis tools, as an admission workaround.

The existing production Harness entrypoint was tested against the new contract
package over Linux-style FIFO and macOS-style spool transport with controlled
model and target peers. It accepted a new local hypothesis with no history, and
delivered an unresolved-parent rejection without retrying the model. No Harness
dispatcher or tool implementation change was needed for these cases.

A currently built image pinned to 0.0.5 will not match an Operator build pinned to
0.0.6; deploy matching rebuilt artifacts. Retained campaign records remain readable.
This work does not replay existing failed campaigns or claim cloud/native Docker
qualification.
