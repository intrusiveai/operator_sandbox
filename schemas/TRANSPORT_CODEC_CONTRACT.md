# Shared transport codecs and state

This stage implements the message-level mechanics shared by Linux FIFOs and macOS
file spools. The normative physical rules remain in
[SHARED_CONTRACT.md](SHARED_CONTRACT.md#4-pipe-envelope-and-channels).
The helpers perform no filesystem I/O and do not qualify an OS/Docker runtime.

## Physical lanes

Lane names are relative to the guest on both transports:

| Lane | Writer → reader | Envelope | Encoded JSON ceiling |
|---|---|---|---|
| `ordinary-in` | Host → guest | Ordinary response | 4 MiB |
| `ordinary-out` | Guest → host | Ordinary request | 4 MiB |
| `control-in` | Host → guest | Host control union | 64 KiB |
| `control-out` | Guest → host | Guest control union | 64 KiB |

`ValidateLaneMessage` / `validate_lane_message` validates the lane's closed envelope,
including installed operation schemas, error codes and narrower per-operation byte
ceilings. It does not correlate a response with its original request; the dispatcher
must still run `ValidateResponse` / `validate_response` before using a reply.
Envelope validation is not startup admission or authorization to perform an effect.

## FIFO framing

`EncodeFrame` / `encode_frame` validates one envelope and emits its four-byte unsigned
big-endian JSON byte length followed by those exact bytes. The prefix is additional
to the JSON ceiling. No newline, canonicalization or text rewriting is performed.

`NewFrameDecoder` / `new_frame_decoder` creates one decoder for a fixed lane.
Its incremental `Feed` / `feed` accepts arbitrary byte chunks, including empty chunks,
split prefixes and split bodies. It validates the declared length immediately upon
receiving all four prefix bytes; zero and oversized lengths fail before allocating
the declared payload. It retains at most one bounded incomplete frame.

Each successful feed returns the number of input bytes consumed and at most one
complete validated envelope. The caller retains any unconsumed suffix and feeds
it again after making processing capacity available. Coalesced frames do not cause
an unbounded result list. An empty chunk means no progress, not EOF. A returned
frame remains independent of subsequent feeds.

Go returns `(consumed, frame, error)`; Python returns `(consumed, frame)` or raises
`ContractError`. An incomplete frame is `nil` / `None`. After any framing or envelope
error, that decoder is permanently closed and cannot be reused. Erroring input
and any remaining suffix are discarded as part of terminal connection handling.

`End` / `end` represents established FIFO EOF or peer loss and is always terminal,
including at a frame boundary. Initial no-writer EOF during FIFO rendezvous belongs
to the runtime's distinct startup phase; do not feed that event to an established
decoder and then attempt to reset it. There is no decoder reset/reconnect API.

## Spool names and envelope capture

`SpoolMessageName` / `spool_message_name` formats a sequence from 0 through 2^53−1
as exactly 20 ASCII decimal digits plus `.json`. Temporary names prepend `.` and
use `.tmp`, for example `.00000000000000000012.tmp`.
`ParseSpoolMessageName` / `parse_spool_message_name` returns the sequence and whether
the name is temporary. It rejects alternate padding, signs, paths, non-ASCII digits,
out-of-range integers, alternate suffixes and trailing newlines.

`ValidateSpoolMessage` / `validate_spool_message` accepts only a ready filename,
checks the lane envelope and requires its `seq` to match the filename. It does not
open or inspect the file. Temporary message files cannot enter dispatch through
this API. `consumed.json` and `.consumed.tmp` are separate fixed control-lane ACK
paths, not message filenames; the runtime handles them through ACK validation.

Readers must still use bounded descriptor-relative no-follow reads, enforce file
types, capture immutable bytes, and reject unexpected directory entries. Producers
must create temporary files exclusively, serialize publication, atomically rename
without replacing ready messages, and account for temporary files in queue limits.
These physical operations are not implemented by the filename helpers.

## Sequence and acknowledgement state

`NewTransportState(role, campaign, launch)` / `new_transport_state(...)` creates a
host or guest state with separate incoming and outgoing ordinary/control positions.
Each begins before sequence zero. The host obtains IDs from its launch assignment.
The guest first validates `bootstrap`, creates state from that message's campaign/
launch IDs, and accepts that same `control-in` sequence-zero message before emitting
its first consumption ACK. It does not read campaign input files to learn transport
identity. The live startup state machine must ensure that bootstrap is first.

One event-loop owner serializes calls; the state object is not concurrency-safe.

| Method | Effect |
|---|---|
| `Accept` / `accept` | Validate an incoming envelope, correct reader/lane, campaign/launch and exact next sequence; record acceptance into the receiver's bounded queue. |
| `RecordPublished` / `record_published` | Record a successfully transferred/published outgoing envelope, correct writer/lane and exact next sequence. |
| `AckBytes` / `ack_bytes` | Encode the receiver's current cumulative consumption ACK. |
| `ApplyAck` / `apply_ack` | Validate a peer ACK against this launch and highest published positions, then advance acknowledgements monotonically. |
| `Acknowledged` / `acknowledged` | Return a copy of the last valid peer-consumed positions for producer cleanup. |
| `Close` / `close` | Permanently stop further message/ACK processing. |

Each lane's sequence is consecutive from zero. Gaps, replays, wrong direction,
wrong campaign/launch, invalid envelopes or attempts to wrap after 2^53−1 close the
state. Failure in one lane stops message/ACK processing for every lane in that state.
Prior acknowledged positions remain inspectable for cleanup. Healthy target restores
retain the same state and counters; a revision change never resets transport.
Revision validity is the dispatcher's responsibility. In particular, a termination
control message is not rejected merely because it names an older revision.

ACK positions begin as `null`. A matching-launch ACK cannot acknowledge beyond the
highest sequence actually published in either outgoing lane. Repeated/lower positions
and later `null` values do not rewind a position. Both positions are checked before
either is changed; a future control ACK cannot partially acknowledge ordinary data.
Wrong-launch or future ACKs close the state. ACKs have no sequence or run revision
and never acknowledge other ACKs.

`RecordPublished` is called only after the physical send/publication succeeds and
before pumping peer ACKs again. `Accept` is called only when the captured message
can enter a reserved processing slot. Queue insertion and recording acceptance must
complete in the same event-loop step, before publishing the consumption ACK. An ACK
proves only that the receiver captured the message; it is not an operation receipt,
successful execution result or cancellation confirmation.

The helpers hold positions, not message queues or physical storage. The runtime
must enforce two ordinary and 16 control outstanding slots, including partial writes
and temporary files, plus their byte bounds. A peer ACK permits cleanup but does
not itself free physical capacity: delete covered producer files before reusing
slots. The runtime must continue pumping ACK/control traffic during long operations.

## Remaining runtime work

These APIs do not open FIFOs, establish rendezvous, read/write/rename spool files,
schedule polling or enforce deadlines. The runtime must implement the existing
10 ms control-priority pump, five-second transfer/ACK/full-queue limits, operation
deadlines, spool-size checks and shutdown cleanup. Empty reads, partial progress and
stale ACKs never renew those deadlines.

On a decoder, state or physical-I/O failure, the runtime closes every decoder/state,
closes admission and follows the required journal/uncertain-effect/Docker termination
path. Separate decoder objects cannot themselves close a Docker container or another
lane. Startup gates, response correlation, durable duplicate handling, target-revision
policy and effect accounting also remain dispatch/runtime responsibilities.

The [73 shared fixtures](fixtures/transport-codec.json) cover fragmented/coalesced
frames, malformed prefixes/envelopes, established EOF, names and lane roles, consecutive
sequences, revision continuity, terminal failures and cumulative ACK behavior. Both
languages additionally test every byte split of a sample frame, maximum sequence
exhaustion and rejection of oversized headers before payload allocation. Regenerate
these vectors with `python3 scripts/generate_transport_fixtures.py`; the generator
uses no production transport helper and introduces no runtime dependency.

The [host transport implementation](../docs/HOST_TRANSPORT.md) now consumes these
helpers for real FIFO/spool I/O, bounded queues, physical ACK cleanup and deadlines.
The Python physical peer is implemented. Complete cross-language process exchanges
and Docker qualification remain outstanding.
