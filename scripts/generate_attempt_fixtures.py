#!/usr/bin/env python3
"""Author attempt traces with explicit expectations; no production imports."""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MAX = 2**53 - 1
D1, D2 = 'sha256:' + '1'*64, 'sha256:' + '2'*64
cases = []


def allocation(raw='{}', request='r1', attempt='a1', index=1, valid=True):
    return dict(action='allocate', raw=raw, request_id=request, attempt_id=attempt,
                allocated_index=index, valid=valid)


def allocator(name, steps, high_watermark=0):
    cases.append(dict(name=name, mode='allocator', high_watermark=high_watermark, steps=steps))


def observe(request='r1', attempt='a1', index=1, digest=D1, replay=False, valid=True, state='observed'):
    return dict(action='observe', request_id=request, attempt_id=attempt, attempt_index=index,
                identity_digest=digest, replay=replay, valid=valid, state=state)


def admit(request='r1', fresh=True, valid=True):
    return dict(action='admit', request_id=request, fresh=fresh, valid=valid)


def resolve(outcome, request='r1', digest=D1, valid=True):
    return dict(action='resolve', request_id=request, outcome=outcome, result_digest=digest, valid=valid)


def ledger(name, steps, high_watermark=0, maximum=100):
    # Each row explicitly states the expected post-step high water/admissions/closed.
    cases.append(dict(name=name, mode='ledger', high_watermark=high_watermark, maximum=maximum,
                      steps=[dict(step, high_watermark=water, admissions=count, closed=closed)
                             for step, water, count, closed in steps]))


allocator('new campaign and corrected submission', [allocation(), allocation(request='r2', attempt='a2', index=2)])
allocator('seed and local rejection retain seven', [allocation(raw='{"missing_fields":true}', index=7), allocation(request='r2', attempt='a2', index=8)], 6)
for raw in ('[]', 'null', 'true', '1', '"args"', '{', '{}{}', '{"x":1,"x":2}'):
    allocator('malformed or nonobject '+raw, [allocation(raw=raw, index=0, valid=False), allocation()])
for field in ('request_id', 'attempt_id', 'attempt_index'):
    allocator('reserved '+field, [allocation(raw=json.dumps({field: None}), index=1, valid=False),
                                 allocation(request='r2', attempt='a2', index=2)])
for request, attempt in [('', 'a1'), ('r1', ''), ('r\n', 'a1'), ('r'*129, 'a1'), ('r1', '../a')]:
    allocator('invalid generated IDs '+repr((request, attempt)), [allocation(request=request, attempt=attempt, index=0, valid=False), allocation()])
allocator('request ID cannot be allocated twice', [allocation(), allocation(attempt='a2', index=0, valid=False), allocation(request='r2', attempt='a2', index=2)])
allocator('attempt ID cannot be allocated twice', [allocation(), allocation(request='r2', index=0, valid=False), allocation(request='r2', attempt='a2', index=2)])
allocator('last safe index then exhaustion', [allocation(index=MAX), allocation(request='r2', attempt='a2', index=0, valid=False)], MAX-1)
allocator('already exhausted', [allocation(index=0, valid=False)], MAX)

ledger('reject seven correct eight and replay original result', [
    (observe(index=7), 7, 0, False),
    (resolve('rejected'), 7, 0, False),
    (admit(valid=False), 7, 0, False),
    (observe('r2', 'a2', 8), 8, 0, False),
    (admit('r2'), 8, 1, False),
    (resolve('succeeded', 'r2'), 8, 1, False),
    (observe('r2', 'a2', 8, replay=True, state='succeeded'), 8, 1, False),
    (admit('r2', fresh=False), 8, 1, False),
    (observe(index=7, replay=True, state='rejected'), 8, 1, False),
], high_watermark=6)
ledger('pending duplicate never gets a second admission', [
    (observe(), 1, 0, False), (observe(replay=True), 1, 0, False),
    (admit(), 1, 1, False), (observe(replay=True, state='admitted'), 1, 1, False),
    (admit(fresh=False), 1, 1, False)])
for changed in (observe(digest=D2, valid=False), observe(attempt='a2', valid=False), observe(index=2, valid=False)):
    ledger('used request conflicts '+str(changed), [(observe(), 1, 0, False), (changed, 1, 0, False)])
for bad in (observe(index=0, valid=False), observe(index=-1, valid=False), observe(index=MAX+1, valid=False),
            observe(request='', valid=False), observe(attempt='a\n', valid=False), observe(digest='sha256:bad', valid=False)):
    ledger('invalid bookkeeping '+str(bad), [(bad, 0, 0, False), (observe(), 1, 0, False)])
ledger('monotonicity and attempt ID uniqueness', [
    (observe(index=3), 3, 0, False), (observe('r2', 'a2', 2, valid=False), 3, 0, False),
    (observe('r2', 'a2', 3, valid=False), 3, 0, False), (observe('r2', 'a1', 4, valid=False), 3, 0, False),
    (observe('r2', 'a2', 9), 9, 0, False)])
ledger('local gaps and failed setup do not refund admission', [
    (observe(index=99), 99, 0, False), (admit(), 99, 1, False), (resolve('failed'), 99, 1, False),
    (observe('r2', 'a2', 101), 101, 1, False), (admit('r2'), 101, 2, False),
    (observe('r3', 'a3', 102), 102, 2, False), (admit('r3', valid=False), 102, 2, False),
    (resolve('rejected', 'r3'), 102, 2, False), (admit(fresh=False), 102, 2, False)], maximum=2)
ledger('unknown outcome blocks new execution but permits saved lookup', [
    (observe(), 1, 0, False), (admit(), 1, 1, False), (resolve('unknown'), 1, 1, True),
    (resolve('unknown'), 1, 1, True), (resolve('succeeded', valid=False), 1, 1, True),
    (observe(replay=True, state='unknown'), 1, 1, True), (admit(valid=False), 1, 1, True),
    (observe('r2', 'a2', 2, valid=False), 1, 1, True)])
ledger('close prevents pending admission and new submissions', [
    (observe(), 1, 0, False), (dict(action='close', valid=True), 1, 0, True),
    (dict(action='close', valid=True), 1, 0, True), (admit(valid=False), 1, 0, True),
    (observe(replay=True), 1, 0, True), (resolve('rejected', valid=False), 1, 0, True)])
ledger('invalid state transitions preserve ledger', [
    (admit(valid=False), 0, 0, False), (resolve('rejected', valid=False), 0, 0, False),
    (observe(), 1, 0, False), (resolve('succeeded', valid=False), 1, 0, False),
    (resolve('unknown', valid=False), 1, 0, False), (admit(), 1, 1, False),
    (resolve('rejected', valid=False), 1, 1, False), (resolve('failed', digest='bad', valid=False), 1, 1, False),
    (resolve('failed'), 1, 1, False), (resolve('failed'), 1, 1, False),
    (resolve('failed', digest=D2, valid=False), 1, 1, False), (resolve('succeeded', valid=False), 1, 1, False)])
ledger('target restore and skipped calls cannot rewind state', [
    (observe(index=7), 7, 0, False), (admit(), 7, 1, False), (resolve('succeeded'), 7, 1, False),
    # Restore/skip produce no bookkeeping event: the same objects remain alive.
    (dict(action='retain', valid=True), 7, 1, False),
    (observe('r2', 'a2', 8), 8, 1, False), (admit('r2'), 8, 2, False)])
ledger('host at final safe index still serves duplicate', [
    (observe(index=MAX), MAX, 0, False), (observe(index=MAX, replay=True), MAX, 0, False),
    (observe('r2', 'a2', 1, valid=False), MAX, 0, False)], high_watermark=MAX-1)

(ROOT/'schemas/fixtures/attempt-bookkeeping.json').write_text(json.dumps(cases, indent=2)+'\n')
print(f'{len(cases)} attempt bookkeeping traces')
