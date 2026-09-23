#!/usr/bin/env python3
"""Independently authored finite-loop traces; no production accounting imports."""
import json
import random
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MAX = 2**53-1
D1, D2 = 'sha256:'+'1'*64, 'sha256:'+'2'*64
DEFAULTS = dict(max_model_turns=300, max_tool_calls=2000, max_tool_calls_per_response=16,
                max_invalid_tool_calls=50, max_consecutive_invalid_tool_calls=5,
                max_read_bytes=268435456, max_no_progress_turns=10)
cases = []


def config(name, overrides='{}', caps='{}', valid=True, expected=None):
    cases.append(dict(name=name, mode='config', overrides=overrides, caps=caps,
                      valid=valid, expected=DEFAULTS if expected is None else expected))


def step(action, expected=None, error='', **args):
    return dict(action=action, expected=expected or {}, error=error, **args)


def trace(name, steps, **limits):
    cases.append(dict(name=name, mode='loop', limits=dict(DEFAULTS, **limits), steps=steps))


def start(count=0, compaction=False):
    return [step('begin_model', compaction=compaction), step('accept_response', count=count)]


def tool(name='snapshot_list', outcome='success'):
    return [step('start_tool', name=name), step('finish_tool', outcome=outcome)]


def turn(*tools, compaction=False):
    return start(len(tools), compaction)+[s for name, outcome in tools for s in tool(name, outcome)]+[step('end_turn')]


def read(offset, size, actual=None, novel=True, source=D1, kind='content'):
    return [step('reserve_read', source_kind=kind, source_id=source, offset=offset, size=size),
            step('settle_read', actual=size if actual is None else actual, result=novel)]


def final_start(remaining=2097152, now=100, deadline=100000):
    return [step('stop', reason='budget-limit'), step('begin_finalization', now=now,
            deadline=deadline, remaining=remaining, final_expected=dict(requests=0,
            conclusion_bytes=0, deadline_ms=min(now+30000, deadline), closed=now>=deadline))]


config('omitted defaults')
for key, default in DEFAULTS.items():
    config(key+' override above default', json.dumps({key:default+1}), expected=dict(DEFAULTS, **{key:default+1}))
    config(key+' policy cap narrows', json.dumps({key:default+1}), json.dumps({key:2}), expected=dict(DEFAULTS, **{key:2}))
    config(key+' policy cap cannot enlarge', '{}', json.dumps({key:default+1}))
    config(key+' safe maximum', json.dumps({key:MAX}), expected=dict(DEFAULTS, **{key:MAX}))
    for value in (0, -1, True, None, 1.5, MAX+1):
        config(key+' invalid override '+repr(value), json.dumps({key:value}), valid=False)
    config(key+' invalid cap', '{}', json.dumps({key:0}), valid=False)
for raw in ('null', '[]', '{"unknown":1}', '{"max_model_turns":1,"max_model_turns":2}',
            '{"max_model_turns":1.00000000000000001}', '{"max_model_turns":"3"}'):
    config('invalid configuration '+raw, raw, valid=False)
config('exact integer exponent', '{"max_model_turns":1e0}', expected=dict(DEFAULTS,max_model_turns=1))
config('unknown policy field', '{}', '{"unexpected":1}', valid=False)
config('nonobject policy', '{}', '[]', valid=False)

trace('last permitted model can finish its batch', start(1)+tool('attempt_execute')+[
    step('inspect', dict(mode='exploring',model_turns=1,tool_calls=1)),
    step('end_turn', dict(mode='finalizing',reason='budget-limit',no_progress_turns=1)),
    step('begin_model', error='stopped')], max_model_turns=1)
trace('last permitted tool finishes and later calls are skipped', start(3)+tool()+[
    step('inspect', dict(mode='exploring',tool_calls=1,queued_calls=2)),
]+tool('unknown','invalid')+[
    step('inspect', dict(mode='finalizing',reason='budget-limit',tool_calls=2,invalid_tool_calls=1,skipped_calls=1,queued_calls=0)),
    step('start_tool', name='attempt_execute', error='stopped'), step('end_turn')], max_tool_calls=2)
trace('oversize response executes no prefix', [step('begin_model'),
    step('accept_response', count=17, error='stopped', expected=dict(model_turns=1,tool_calls=0,skipped_calls=17,reason='budget-limit')),
    step('start_tool',name='attempt_execute',error='stopped'), step('end_turn')])
trace('exact batch ceiling allowed', start(2)+tool()+tool()+[step('end_turn',dict(mode='exploring',tool_calls=2))], max_tool_calls_per_response=2)
trace('five consecutive invalid calls', start(6)+tool('unknown','invalid')*5+[
    step('inspect',dict(reason='harness-error',invalid_tool_calls=5,consecutive_invalid_tool_calls=5,tool_calls=5,skipped_calls=1)),step('end_turn')])
trace('fifty cumulative invalid calls despite successes',
      turn(('unknown','invalid'),('snapshot_list','success'))*49 +
      start(2)+tool('unknown','invalid')+[
          step('inspect',dict(reason='harness-error',invalid_tool_calls=50,consecutive_invalid_tool_calls=1,tool_calls=99,skipped_calls=1))],
      max_no_progress_turns=100)
trace('records preflight rejection and no-tool turns do not reset invalid streak',
      turn(('unknown','invalid'))+turn(('engine.record_append','success'),('restore_request','preflight-rejected'))+turn()+[
          step('inspect',dict(invalid_tool_calls=1,consecutive_invalid_tool_calls=1,no_progress_turns=3))]+
      turn(('snapshot_list','success'))+[
          step('inspect',dict(invalid_tool_calls=1,consecutive_invalid_tool_calls=0,no_progress_turns=4))])
trace('ten empty or refusal turns stop exploration', turn()*10+[
    step('inspect',dict(reason='no-useful-next-experiment',model_turns=10,no_progress_turns=10)),step('begin_model',error='stopped')])
trace('compaction consumes model turn and gives no progress', turn(compaction=True)+[
    step('inspect',dict(model_turns=1,no_progress_turns=1,tool_calls=0))])
trace('compaction cannot execute returned tools', [step('begin_model',compaction=True),
    step('accept_response',count=1,error='protocol',expected=dict(mode='closed',reason='hard-stop',model_turns=1,tool_calls=0))])
trace('known negative experiment counts once; replay not progress', turn()+start(1)+[
    step('start_tool',name='engine.attempt_execute'), step('experiment',receipt='receipt-1',result=True),
    step('finish_tool',outcome='success'), step('end_turn',dict(no_progress_turns=0))]+start(1)+[
    step('start_tool',name='attempt_execute'), step('experiment',receipt='receipt-1',result=False),
    step('finish_tool',outcome='success'),step('end_turn',dict(no_progress_turns=1))])
trace('first content commitment is progress; duplicates are not', turn()+start(1)+[
    step('start_tool',name='artifact_commit'),step('payload',digest=D1,result=True),
    step('finish_tool',outcome='success'),step('end_turn',dict(no_progress_turns=0))]+start(1)+[
    step('start_tool',name='artifact_commit'),step('payload',digest=D1,result=False),
    step('finish_tool',outcome='success'),step('end_turn',dict(no_progress_turns=1))])
trace('progress metadata is not a progress event', turn(('record_append','success'),('snapshot_request','success'))*2+[
    step('inspect',dict(reason='no-useful-next-experiment',no_progress_turns=2))],max_no_progress_turns=2)
trace('successful restore skips batch without new tool charges', start(3)+tool('restore_request','restored')+[
    step('inspect',dict(tool_calls=1,queued_calls=0,skipped_calls=2,mode='exploring')),
    step('start_tool',name='attempt_execute',error='protocol'),step('end_turn',dict(no_progress_turns=1))]+
    turn(('snapshot_list','success'))+[step('inspect',dict(model_turns=2,tool_calls=2,skipped_calls=0,no_progress_turns=2))])
trace('restore preflight rejection allows next tool', turn(('restore_request','preflight-rejected'),('attempt_execute','failed'))+[
    step('inspect',dict(tool_calls=2,skipped_calls=0,invalid_tool_calls=0,no_progress_turns=1,mode='exploring'))])
trace('initial reads charged and cannot be rediscovered for progress', read(0,4)+start(1)+[
    step('start_tool',name='reference_read')]+read(0,4,novel=False)+[
    step('finish_tool',outcome='success'),step('end_turn',dict(read_bytes=8,no_progress_turns=1))])
trace('range unions handle overlap disjointness and bridging', start(1)+[
    step('start_tool',name='reference_read')]+read(0,4)+read(8,4)+read(2,8)+read(0,12,novel=False)+[
    step('finish_tool',outcome='success'),step('end_turn',dict(read_bytes=28,no_progress_turns=0))])
trace('same bytes different verified observation entry are new progress', start(1)+[
    step('start_tool',name='observation_read')]+read(0,3,kind='observation')+read(0,3,novel=False,kind='observation')+
    read(0,3,source=D2,kind='observation')+[step('finish_tool',outcome='success'),step('end_turn',dict(read_bytes=9,no_progress_turns=0))])
trace('EOF and unavailable refund reservations and give no progress', start(1)+[
    step('start_tool',name='reference_read'),
    step('reserve_read',source_kind='content',source_id=D1,offset=0,size=8,expected=dict(read_bytes=0,reserved_read_bytes=8)),
    step('settle_read',actual=0,result=False,expected=dict(read_bytes=0,reserved_read_bytes=0))]+read(0,8,actual=2)+[
    step('finish_tool',outcome='success'),step('end_turn',dict(read_bytes=2,reserved_read_bytes=0,mode='exploring'))],max_read_bytes=8)
trace('read ceiling allows last result but skips following calls',start(2)+[
    step('start_tool',name='observation_read')]+read(0,4)+[
    step('inspect',dict(mode='finalizing',reason='budget-limit',read_bytes=4,skipped_calls=1)),
    step('finish_tool',outcome='success'),step('end_turn',dict(no_progress_turns=0))],max_read_bytes=4)
trace('oversize read is rejected without delivery or truncation',read(0,3)+[
    step('reserve_read',source_kind='content',source_id=D1,offset=3,size=2,error='stopped',
         expected=dict(reason='budget-limit',read_bytes=3,reserved_read_bytes=0)),
    step('settle_read',actual=1,error='protocol')],max_read_bytes=4)
trace('unknown read closes exploration and retains reservation',[
    step('reserve_read',source_kind='content',source_id=D1,offset=0,size=9),
    step('hard_stop',expected=dict(mode='closed',read_bytes=0,reserved_read_bytes=9)),
    step('settle_read',actual=0,error='protocol'),step('begin_model',error='stopped'),
    step('begin_finalization',now=0,deadline=30000,remaining=100,error='protocol')])
trace('forged progress and invalid settlement cannot change accounting',[
    step('experiment',receipt='fake',error='protocol'),step('payload',digest=D1,error='protocol'),
    step('settle_read',actual=1,error='protocol'),
    step('reserve_read',source_kind='content',source_id=D1,offset=0,size=4),
    step('settle_read',actual=5,error='protocol',expected=dict(read_bytes=0,reserved_read_bytes=4)),
    step('settle_read',actual=0,result=False,expected=dict(read_bytes=0,reserved_read_bytes=0))])
trace('ordering errors do not charge extra work',[
    step('start_tool',name='attempt_execute',error='protocol'),step('end_turn',error='protocol'),step('begin_model'),
    step('begin_model',error='protocol',expected=dict(model_turns=1)),step('accept_response',count=1),
    step('start_tool',name='unknown'),step('start_tool',name='attempt_execute',error='protocol',expected=dict(tool_calls=1)),
    step('finish_tool',outcome='restored',error='protocol'),step('end_turn',error='protocol'),
    step('finish_tool',outcome='invalid'),step('finish_tool',outcome='success',error='protocol'),step('end_turn')])
trace('host budgets narrow and cannot be widened',[
    step('narrow',models=1,reads=4),step('narrow',models=300,reads=1000)]+turn()+[
    step('inspect',dict(reason='budget-limit',model_turns=1)),step('begin_model',error='stopped')])
trace('zero host model allowance preserves already admitted batch',start(1)+[
    step('narrow',models=0,reads=100)]+tool()+[step('end_turn',dict(reason='budget-limit',model_turns=1,tool_calls=1))])
trace('zero host read allowance stops immediately',[
    step('narrow',models=10,reads=0,expected=dict(reason='budget-limit',model_turns=0)),step('begin_model',error='stopped')])
trace('full safe integer batch can be skipped without overflow',start(MAX)+tool('restore_request','restored')+[
    step('inspect',dict(skipped_calls=MAX-1,tool_calls=1)),step('end_turn')]+start(MAX)+tool('restore_request','restored')+[
    step('inspect',dict(skipped_calls=MAX-1,tool_calls=2)),step('end_turn')],max_tool_calls_per_response=MAX)
trace('read offset overflow rejected before reservation',[
    step('reserve_read',source_kind='content',source_id=D1,offset=MAX,size=1,error='protocol',expected=dict(read_bytes=0,reserved_read_bytes=0))])
trace('read range adjacent to safe integer boundary',read(MAX-1,1)+read(MAX-1,1,novel=False)+[
    step('inspect',dict(read_bytes=2,mode='exploring'))])
# Independently model delivered byte positions with sets rather than interval
# merging. This catches gaps, nested overlaps and source-isolation mistakes.
rng = random.Random(871)
seen, delivered = {}, 0
range_steps = start(1)+[step('start_tool',name='reference_read')]
for _ in range(80):
    key = (rng.choice(['content','observation']),rng.choice([D1,D2]))
    offset, size = rng.randrange(24), rng.randrange(1,13)
    actual = rng.randrange(size+1)
    positions = set(range(offset,offset+actual))
    prior = seen.setdefault(key,set())
    novel = bool(positions-prior)
    prior.update(positions)
    delivered += actual
    range_steps += read(offset,size,actual=actual,novel=novel,source=key[1],kind=key[0])
range_steps += [step('finish_tool',outcome='success'),step('end_turn',dict(read_bytes=delivered,no_progress_turns=0))]
trace('mixed read ranges match independent byte-set oracle',range_steps)
trace('host stop with model generation outstanding retains admission',[
    step('begin_model'),step('hard_stop',expected=dict(model_turns=1,mode='closed',reason='hard-stop')),
    step('accept_response',count=0,error='stopped'),step('begin_model',error='stopped')])

trace('finalization exact sixteen requests and no seventeenth',final_start()+[
    step('charge',kind='conclusion-record',bytes=0,now=100+i) for i in range(16)]+[
    step('charge',kind='stop',bytes=0,now=116,error='stopped',final_expected=dict(requests=16,conclusion_bytes=0,closed=True))])
trace('finalization exact byte limit still permits record and stop',final_start()+[
    step('charge',kind='conclusion-artifact',bytes=2097152,now=100),
    step('charge',kind='conclusion-record',bytes=0,now=101),step('charge',kind='stop',bytes=0,now=102,
         final_expected=dict(requests=3,conclusion_bytes=2097152,closed=False)),
    step('charge',kind='conclusion-artifact',bytes=1,now=103,error='stopped',final_expected=dict(requests=3,closed=True))])
trace('finalization host artifact allowance wins',final_start(remaining=4)+[
    step('charge',kind='conclusion-artifact',bytes=5,now=100,error='stopped',final_expected=dict(requests=0,conclusion_bytes=0,closed=True))])
trace('zero artifact allowance permits unavailable conclusion stop',final_start(remaining=0)+[
    step('charge',kind='stop',bytes=0,now=100,final_expected=dict(requests=1,conclusion_bytes=0,closed=False))])
trace('finalization thirty seconds never extends',final_start()+[
    step('charge',kind='conclusion-record',bytes=0,now=30099),step('check_time',now=30100,error='stopped',
        final_expected=dict(deadline_ms=30100,closed=True))])
trace('campaign deadline wins over thirty second allowance',final_start(deadline=120)+[
    step('check_time',now=119),step('charge',kind='stop',bytes=0,now=120,error='stopped',final_expected=dict(requests=0,closed=True))])
trace('expired campaign permits no finalization requests',final_start(deadline=99)+[
    step('charge',kind='stop',bytes=0,now=100,error='stopped')])
trace('finalization cannot restart or admit exploration',final_start()+[
    step('begin_finalization',now=200,deadline=100000,remaining=2097152,error='protocol'),
    step('begin_model',error='stopped'),step('start_tool',name='attempt_execute',error='stopped'),
    step('charge',kind='attempt_execute',bytes=0,now=200,error='protocol',final_expected=dict(requests=0,deadline_ms=30100)),
    step('charge',kind='stop',bytes=1,now=200,error='protocol')])
trace('hard stop overrides active finalization',final_start()+[
    step('hard_stop',expected=dict(mode='closed',reason='hard-stop'),final_expected=dict(closed=True)),
    step('charge',kind='stop',bytes=0,now=100,error='stopped'),
    step('begin_finalization',now=101,deadline=100000,remaining=100,error='protocol')])
trace('monotonic clock reversal closes finalization',final_start()+[
    step('check_time',now=99,error='protocol',final_expected=dict(closed=True)),step('check_time',now=100,error='stopped')])

(ROOT/'schemas/fixtures/harness-loop-accounting.json').write_text(json.dumps(cases, indent=2)+'\n')
print(f'{len(cases)} loop limit/accounting cases')
