# Durable attempt receipts

The host reserves publication capacity before native dispatch. It writes verified,
filtered feedback in ordered journal members, stages an immutable receipt index,
then adopts that index in the final `attempt.resolved` event. Only this last event
makes the receipt available to readers. A failed write closes execution; staged
files alone never authorize a reply or another experiment.

The index records original campaign/session/revision attribution, native-to-public
entry mappings, artifact descriptors and confirmed injection handles. A handle
requires a successful arm record; its automatic-deletion flag requires a successful
delete record. Repeated publication must match all metadata and byte digests and
does not write again. Receipt IDs cannot be reused across attempts.

Reads verify private, no-follow journal members and the full artifact digest before
slicing a bounded chunk. Admission, campaign, receipt, entry and current visibility
checks precede byte access. Healthy restore preserves the original source and does
not redirect old feedback to the replacement target. Missing or corrupt optional
content returns explicit unavailability without bytes or EOF. Corrupt receipt
metadata is a terminal integrity failure. Whole-journal inspection still reports
any missing committed content as incomplete evidence.

Publication is a live host-library operation. Recovery is inspection and reporting
only; no constructor resumes execution from staged or completed records. Broker
integration must reserve capacity, publish, and only then emit the guest response.
