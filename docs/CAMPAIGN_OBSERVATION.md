# Campaign observation

These commands read campaign-local journals and require access to the private
state root. They never contact Docker, Interceptor, a model provider or a secret
store, acquire the writer lock, or alter campaign execution.

```sh
operatorctl campaign status --campaign CAMPAIGN_ID
operatorctl campaign logs --campaign CAMPAIGN_ID --after 0 --limit 1000
operatorctl campaign wait --campaign CAMPAIGN_ID --timeout 35m
```

All three accept `--config /absolute/config.yaml` or an explicit
`--state-root /absolute/data`. An explicit state root bypasses the default
configuration; an explicitly supplied configuration must load successfully.

## Results

`status` emits one JSON receipt with campaign/launch/manifest identity, verified
sequence/byte totals, latest recorded revision/event kind and `terminal_recorded`.
`execution_state` is `closed` only when the verified prefix includes
`launch.terminal`; otherwise it is `unknown`. These are persisted facts, not
process-liveness probes. A terminal record does not imply a successful experiment,
confirmed container removal or complete evidence; those have separate result data.

`logs` emits JSON lines: up to `--limit` event objects followed by one observation
receipt. The default is 1,000 events, with a maximum of 10,000 per page. `--after`
is an exclusive, campaign-wide sequence cursor. The receipt's `next_after` is the
last emitted sequence; `more_events` indicates whether the captured prefix has
more events. Revisions do not reset the cursor. Content descriptors are included;
retained content bodies are not printed automatically. Events may contain
sensitive target data and must be handled as campaign evidence.

Consumers MUST check the command exit code and final `prefix_verified` receipt.
Individually verified streamed events before an error do not establish a complete
page. Each call captures one committed head; later appends appear on the next
call. Pending tails are outside the observation. Strict recovery inspection is a
separate operation.

`wait` checks once per second until a verified `launch.terminal` appears, then
emits one receipt. Its default timeout is 35 minutes; a positive duration up to
24 hours is accepted. Timeout, SIGINT, SIGTERM, or context cancellation ends only
the observer, returning a nonzero exit status. A dead worker without a terminal
record cannot be mistaken for completed finalization. Administrative termination
remains available independently.

Exit 0 means the requested observation completed, not that the campaign succeeded.
Exit 1 indicates read/output failure, cancellation or timeout; exit 2 indicates
invalid arguments or configuration. A timed-out wait may include the last valid
snapshot; the nonzero exit code still means terminal completion was not observed.

Tests exercise real active journals, pagination, terminal records, cancellation,
timeout, changed committed data, and continued writer operation after observation.

Run-directory lookups and pre-journal start status MUST follow
[Campaign startup](CAMPAIGN_START.md).
