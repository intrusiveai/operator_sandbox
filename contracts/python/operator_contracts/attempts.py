"""Campaign attempt numbering and admission state; no persistence or native effects."""
from dataclasses import dataclass, replace
import re

from .validation import ContractError, decode, ORDINARY_LIMIT, MAX_SAFE_INTEGER
from .startup import require

_ID = re.compile(r'[A-Za-z0-9][A-Za-z0-9._:-]{0,127}', re.ASCII)
_DIGEST = re.compile(r'sha256:[0-9a-f]{64}', re.ASCII)


def _matches(pattern, value):
    return type(value) is str and pattern.fullmatch(value) is not None


def _integer(value, minimum):
    return type(value) is int and minimum <= value <= MAX_SAFE_INTEGER


@dataclass(frozen=True)
class AttemptAllocation:
    request_id: str
    attempt_id: str
    attempt_index: int


class AllocationError(ContractError):
    """Argument rejection AFTER reservation: retain this allocation in call history."""
    def __init__(self, allocation):
        super().__init__('contract protocol violation')
        self.allocation = allocation


class AttemptAllocator:
    """One campaign/serialized dispatcher; retain across target restores."""
    def __init__(self, high_watermark=0):
        require(_integer(high_watermark, 0))
        self._high_watermark = high_watermark
        self._requests, self._attempts = set(), set()

    @property
    def high_watermark(self):
        return self._high_watermark

    def allocate(self, raw, request_id, attempt_id):
        """Only new dispatched calls; resolve duplicates from saved history first."""
        args = decode(raw, ORDINARY_LIMIT)
        require(type(args) is dict and _matches(_ID, request_id) and _matches(_ID, attempt_id))
        require(request_id not in self._requests and attempt_id not in self._attempts)
        if self._high_watermark == MAX_SAFE_INTEGER:
            raise ContractError('contract encoding limit exceeded')
        self._high_watermark += 1
        self._requests.add(request_id)
        self._attempts.add(attempt_id)
        allocation = AttemptAllocation(request_id, attempt_id, self._high_watermark)
        if any(key in args for key in ('request_id', 'attempt_id', 'attempt_index')):
            raise AllocationError(allocation)
        return allocation


@dataclass(frozen=True)
class AttemptSubmission(AttemptAllocation):
    # Host-computed COMPLETE operation identity, never the guest's payload digest.
    identity_digest: str


@dataclass(frozen=True)
class AttemptRecord:
    submission: AttemptSubmission
    state: str = 'observed'
    result_digest: str = ''


class AttemptLedger:
    """In-memory state only. Persist transitions before responses/native effects.

    Persistence failure must close execution. The runtime bounds submission count.
    Never replace this instance with restored target history.
    """
    def __init__(self, high_watermark=0, maximum_admissions=100):
        require(_integer(high_watermark, 0) and _integer(maximum_admissions, 1))
        self._high_watermark, self._maximum = high_watermark, maximum_admissions
        self._admitted, self._closed = 0, False
        self._records, self._attempts = {}, set()

    @property
    def high_watermark(self):
        return self._high_watermark

    @property
    def admissions(self):
        return self._admitted

    @property
    def closed(self):
        return self._closed

    def close(self):
        self._closed = True

    def lookup(self, request_id):
        return self._records.get(request_id)

    def observe(self, submission):
        """Return (record, replay); replay never permits another execution."""
        require(type(submission) is AttemptSubmission)
        s = submission
        require(_matches(_ID, s.request_id) and _matches(_ID, s.attempt_id)
                and _integer(s.attempt_index, 1) and _matches(_DIGEST, s.identity_digest))
        previous = self._records.get(s.request_id)
        if previous is not None:
            require(previous.submission == s)
            return previous, True
        require(not self._closed and s.attempt_index > self._high_watermark
                and s.attempt_id not in self._attempts)
        record = AttemptRecord(s)
        self._records[s.request_id] = record
        self._attempts.add(s.attempt_id)
        self._high_watermark = s.attempt_index
        return record, False

    def admit(self, request_id):
        """True only for new admission; persist intent before any native effect."""
        r = self._records.get(request_id)
        require(r is not None and not self._closed and r.state != 'rejected')
        if r.state != 'observed':
            return False
        if self._admitted == self._maximum:
            raise ContractError('contract encoding limit exceeded')
        self._records[request_id] = replace(r, state='admitted')
        self._admitted += 1
        return True

    def resolve(self, request_id, outcome, result_digest):
        r = self._records.get(request_id)
        require(r is not None and _matches(_DIGEST, result_digest))
        if r.state == outcome and r.result_digest == result_digest:
            return
        require(not self._closed)
        require((r.state == 'observed' and outcome == 'rejected') or
                (r.state == 'admitted' and outcome in ('succeeded', 'failed', 'unknown')))
        self._records[request_id] = replace(r, state=outcome, result_digest=result_digest)
        if outcome == 'unknown':
            self._closed = True
