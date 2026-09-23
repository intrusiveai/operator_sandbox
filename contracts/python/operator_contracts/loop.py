"""Serialized campaign accounting. Call only from trusted runtime transitions."""
from .attempts import _integer, _matches, _DIGEST, _ID
from .startup import require
from .validation import ContractError, decode, CONTROL_LIMIT, MAX_SAFE_INTEGER

_DEFAULTS = dict(max_model_turns=300, max_tool_calls=2000, max_tool_calls_per_response=16,
                 max_invalid_tool_calls=50, max_consecutive_invalid_tool_calls=5,
                 max_read_bytes=268435456, max_no_progress_turns=10)
_LIMIT_SCHEMA = 'urn:operator:schema:harness-loop-limits:v1alpha1'


class LoopStopped(ContractError):
    def __init__(self):
        super().__init__('contract exploration stopped')


def resolve_harness_limits(protocol, overrides=b'{}', caps=b'{}'):
    """Resolve admin defaults, then apply only explicitly supplied policy caps."""
    parts = []
    for raw in (overrides, caps):
        value = decode(raw, CONTROL_LIMIT)
        require(type(value) is dict)
        protocol._catalog.validate_value(_LIMIT_SCHEMA, dict(_DEFAULTS, **value))
        parts.append(value)
    result = dict(_DEFAULTS, **parts[0])
    for key, cap in parts[1].items():
        result[key] = min(result[key], cap)
    return result


class HarnessLoop:
    """One campaign/event-loop owner. No I/O, durable admissions or model claims.

    Duplicate operations bypass accounting through the runtime's saved-result
    lookup. Call begin_model only for a new admitted generation and start_tool
    only for a newly dispatched model-facing call, never internal upload parts.
    """
    def __init__(self, limits):
        require(type(limits) is dict and limits.keys() == _DEFAULTS.keys()
                and all(_integer(v, 1) for v in limits.values()))
        self._limits = dict(limits)
        self._state = dict(mode='exploring', reason='', phase='idle', active_tool='',
                           model_turns=0, tool_calls=0, invalid_tool_calls=0,
                           consecutive_invalid_tool_calls=0, read_bytes=0,
                           reserved_read_bytes=0, no_progress_turns=0,
                           queued_calls=0, skipped_calls=0)
        self._progress = self._compaction = False
        self._read = None
        self._finalization = None
        self._ranges, self._experiments, self._payloads = {}, set(), set()

    def snapshot(self):
        return dict(self._state)

    def _exploring(self):
        if self._state['mode'] != 'exploring':
            raise LoopStopped()

    def _stop(self, reason):
        s = self._state
        if s['mode'] == 'exploring':
            s['mode'], s['reason'] = 'finalizing', reason
            s['skipped_calls'] += s['queued_calls']
            s['queued_calls'] = 0

    def stop(self, reason):
        """Trusted external graceful policy/budget decision; never a model claim."""
        require(reason in ('budget-limit', 'harness-error', 'no-useful-next-experiment'))
        self._stop(reason)

    def hard_stop(self):
        """Host stop/unknown effect/channel failure bypass all graceful work."""
        s = self._state
        s['mode'], s['reason'] = 'closed', 'hard-stop'
        s['skipped_calls'] += s['queued_calls']
        s['queued_calls'] = 0
        # Keep pending read reservation charged; do not refund uncertain delivery.
        if self._finalization is not None:
            self._finalization.close()

    def narrow_remaining(self, model_turns, read_bytes):
        """Apply a verified host remaining-budget update at a quiescent boundary."""
        self._exploring()
        s = self._state
        require(_integer(model_turns, 0) and _integer(read_bytes, 0)
                and not s['active_tool'] and self._read is None and s['phase'] != 'model')
        for key, used, remaining in (('max_model_turns', s['model_turns'], model_turns),
                                     ('max_read_bytes', s['read_bytes'], read_bytes)):
            self._limits[key] = min(self._limits[key], used + remaining)
        if self._limits['max_read_bytes'] == s['read_bytes'] or (
                s['phase'] == 'idle' and self._limits['max_model_turns'] == s['model_turns']):
            self._stop('budget-limit')

    def begin_model(self, compaction=False):
        self._exploring()
        s = self._state
        require(type(compaction) is bool and s['phase'] == 'idle' and self._read is None)
        if s['model_turns'] >= self._limits['max_model_turns']:
            self._stop('budget-limit')
            raise LoopStopped()
        s['model_turns'] += 1
        s['phase'] = 'model'
        s['skipped_calls'] = 0 # Disposition of this batch, not an unbounded total.
        self._compaction, self._progress = compaction, False

    def accept_response(self, tool_count):
        self._exploring()
        s = self._state
        require(s['phase'] == 'model' and _integer(tool_count, 0))
        if self._compaction and tool_count:
            self.hard_stop()
            raise ContractError('contract message consistency check failed')
        s['phase'], s['queued_calls'] = 'batch', tool_count
        if tool_count > self._limits['max_tool_calls_per_response']:
            self._stop('budget-limit')
            raise LoopStopped()

    def start_tool(self, name):
        self._exploring()
        s = self._state
        require(_matches(_ID, name) and s['phase'] == 'batch' and s['queued_calls'] > 0
                and not s['active_tool'] and self._read is None)
        if s['tool_calls'] >= self._limits['max_tool_calls']:
            self._stop('budget-limit')
            raise LoopStopped()
        s['tool_calls'] += 1
        s['queued_calls'] -= 1
        s['active_tool'] = name

    def finish_tool(self, outcome):
        s = self._state
        require(s['mode'] != 'closed' and s['active_tool'] and self._read is None
                and outcome in ('success', 'invalid', 'preflight-rejected', 'failed', 'restored'))
        name = s['active_tool'].removeprefix('engine.')
        require(outcome != 'restored' or name == 'restore_request')
        if outcome == 'invalid':
            s['invalid_tool_calls'] += 1
            s['consecutive_invalid_tool_calls'] += 1
        elif outcome in ('success', 'restored') and name != 'record_append':
            s['consecutive_invalid_tool_calls'] = 0
        s['active_tool'] = ''
        if outcome == 'restored':
            s['skipped_calls'] += s['queued_calls']
            s['queued_calls'] = 0
        if s['invalid_tool_calls'] >= self._limits['max_invalid_tool_calls'] or (
                s['consecutive_invalid_tool_calls'] >= self._limits['max_consecutive_invalid_tool_calls']):
            self._stop('harness-error')
        elif s['tool_calls'] >= self._limits['max_tool_calls']:
            self._stop('budget-limit')

    def reserve_read(self, source_kind, source_id, offset, size):
        self._exploring()
        s = self._state
        require(self._read is None and _integer(offset, 0) and _integer(size, 1)
                and offset <= MAX_SAFE_INTEGER-size)
        require((source_kind == 'content' and _matches(_DIGEST, source_id)) or
                (source_kind == 'observation' and _matches(_DIGEST, source_id)))
        require(s['phase'] == 'idle' or (s['phase'] == 'batch' and s['active_tool']))
        if size > self._limits['max_read_bytes']-s['read_bytes']:
            self._stop('budget-limit')
            raise LoopStopped()
        self._read = (source_kind, source_id, offset, size)
        s['reserved_read_bytes'] = size

    def settle_read(self, actual):
        s = self._state
        require(s['mode'] != 'closed' and self._read is not None and _integer(actual, 0)
                and actual <= self._read[3])
        kind, identity, offset, _ = self._read
        s['read_bytes'] += actual
        s['reserved_read_bytes'] = 0
        self._read = None
        novel = False
        if actual:
            end = offset + actual
            ranges = self._ranges.get((kind, identity), [])
            covered = sum(max(0, min(end, b)-max(offset, a)) for a, b in ranges)
            novel = covered < actual
            merged = []
            for a, b in sorted(ranges + [(offset, end)]):
                if merged and a <= merged[-1][1]:
                    merged[-1] = (merged[-1][0], max(b, merged[-1][1]))
                else:
                    merged.append((a, b))
            self._ranges[(kind, identity)] = merged
            if novel and s['phase'] == 'batch' and not self._compaction:
                self._progress = True
        if s['read_bytes'] == self._limits['max_read_bytes']:
            self._stop('budget-limit')
        return novel

    def experiment_completed(self, receipt_id):
        s = self._state
        require(s['mode'] != 'closed' and s['phase'] == 'batch'
                and s['active_tool'].removeprefix('engine.') == 'attempt_execute' and _matches(_ID, receipt_id))
        novel = receipt_id not in self._experiments
        self._experiments.add(receipt_id)
        self._progress |= novel
        return novel

    def payload_committed(self, digest):
        s = self._state
        require(s['mode'] != 'closed' and s['phase'] == 'batch' and s['active_tool']
                and _matches(_DIGEST, digest))
        novel = digest not in self._payloads
        self._payloads.add(digest)
        self._progress |= novel
        return novel

    def end_turn(self):
        s = self._state
        require(s['mode'] != 'closed' and s['phase'] == 'batch' and not s['active_tool']
                and not s['queued_calls'] and self._read is None)
        s['no_progress_turns'] = 0 if self._progress and not self._compaction else s['no_progress_turns']+1
        s['phase'] = 'idle'
        if s['no_progress_turns'] >= self._limits['max_no_progress_turns']:
            self._stop('no-useful-next-experiment')
        elif s['model_turns'] >= self._limits['max_model_turns']:
            self._stop('budget-limit')

    def begin_finalization(self, now_ms, campaign_deadline_ms, artifact_remaining):
        s = self._state
        require(s['mode'] == 'finalizing' and s['phase'] == 'idle'
                and self._read is None and self._finalization is None)
        budget = FinalizationBudget(now_ms, campaign_deadline_ms, artifact_remaining)
        self._finalization = budget
        return budget


class FinalizationBudget:
    """Trusted conclusion-only requests; caller enforces payload/receipt authority.

    Times are integer milliseconds from one monotonic clock. Check expiry even
    without requests; this object schedules no timer and never terminates Docker.
    """
    def __init__(self, started_ms, campaign_deadline_ms, artifact_remaining):
        require(all(_integer(v, 0) for v in (started_ms, campaign_deadline_ms, artifact_remaining)))
        self._last = started_ms
        self._deadline = min(started_ms+30000, campaign_deadline_ms)
        self._bytes_limit = min(2 << 20, artifact_remaining)
        self._requests = self._bytes = 0
        self._closed = started_ms >= self._deadline

    def snapshot(self):
        return dict(requests=self._requests, conclusion_bytes=self._bytes,
                    deadline_ms=self._deadline, closed=self._closed)

    def close(self):
        self._closed = True

    def check_time(self, now_ms):
        if self._closed:
            raise LoopStopped()
        if not _integer(now_ms, 0) or now_ms < self._last:
            self.close()
            raise ContractError('contract message consistency check failed')
        self._last = now_ms
        if now_ms >= self._deadline:
            self.close()
            raise LoopStopped()

    def charge(self, kind, conclusion_bytes, now_ms):
        """kind is verified conclusion-artifact/conclusion-record/stop, not a claim.

        Charge distinct content bytes once before transfer, including any existing
        upload continued in finalization. Every new ordinary request costs a slot.
        """
        self.check_time(now_ms)
        require(kind in ('conclusion-artifact', 'conclusion-record', 'stop')
                and _integer(conclusion_bytes, 0)
                and (kind == 'conclusion-artifact' or conclusion_bytes == 0))
        if self._requests == 16 or conclusion_bytes > self._bytes_limit-self._bytes:
            self.close()
            raise LoopStopped()
        self._requests += 1
        self._bytes += conclusion_bytes
