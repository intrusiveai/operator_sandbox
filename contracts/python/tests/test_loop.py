import json
from pathlib import Path
import unittest

from operator_contracts import Protocol, ContractError
from operator_contracts.loop import HarnessLoop, LoopStopped, resolve_harness_limits

ROOT = Path(__file__).resolve().parents[3]


class LoopTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.protocol = Protocol(ROOT/'schemas')

    def assert_projection(self, actual, expected):
        for key, value in expected.items():
            self.assertEqual(actual[key], value, key)

    def test_shared_loop_accounting(self):
        cases = json.loads((ROOT/'schemas/fixtures/harness-loop-accounting.json').read_text())
        for case in cases:
            with self.subTest(case=case['name']):
                if case['mode'] == 'config':
                    def resolve():
                        return resolve_harness_limits(self.protocol, case['overrides'].encode(), case['caps'].encode())
                    if case['valid']:
                        self.assertEqual(resolve(), case['expected'])
                    else:
                        with self.assertRaises(ContractError): resolve()
                    continue
                self.assertEqual(case['mode'], 'loop')
                loop = HarnessLoop(case['limits'])
                budget = None
                for index, s in enumerate(case['steps']):
                    with self.subTest(step=index, action=s['action']):
                        def run():
                            nonlocal budget
                            action = s['action']
                            if action == 'begin_model': return loop.begin_model(s.get('compaction',False))
                            if action == 'accept_response': return loop.accept_response(s['count'])
                            if action == 'start_tool': return loop.start_tool(s['name'])
                            if action == 'finish_tool': return loop.finish_tool(s['outcome'])
                            if action == 'reserve_read': return loop.reserve_read(s['source_kind'],s['source_id'],s['offset'],s['size'])
                            if action == 'settle_read': return loop.settle_read(s['actual'])
                            if action == 'experiment': return loop.experiment_completed(s['receipt'])
                            if action == 'payload': return loop.payload_committed(s['digest'])
                            if action == 'end_turn': return loop.end_turn()
                            if action == 'narrow': return loop.narrow_remaining(s['models'],s['reads'])
                            if action == 'stop': return loop.stop(s['reason'])
                            if action == 'hard_stop': return loop.hard_stop()
                            if action == 'begin_finalization':
                                budget = loop.begin_finalization(s['now'],s['deadline'],s['remaining'])
                                return
                            if action == 'charge': return budget.charge(s['kind'],s['bytes'],s['now'])
                            if action == 'check_time': return budget.check_time(s['now'])
                            if action == 'inspect': return
                            self.fail('unknown fixture action')
                        if s['error']:
                            with self.assertRaises(ContractError) as caught: run()
                            self.assertEqual(isinstance(caught.exception, LoopStopped), s['error']=='stopped')
                        else:
                            result = run()
                            if 'result' in s: self.assertEqual(result,s['result'])
                        self.assert_projection(loop.snapshot(),s['expected'])
                        if 'final_expected' in s: self.assert_projection(budget.snapshot(),s['final_expected'])

    def test_isolation_and_invalid_limits(self):
        limits = resolve_harness_limits(self.protocol)
        loop = HarnessLoop(limits)
        limits['max_model_turns'] = 1
        loop.snapshot()['model_turns'] = 100
        for _ in range(2):
            loop.begin_model(); loop.accept_response(0); loop.end_turn()
        self.assertEqual(loop.snapshot()['model_turns'],2)
        self.assertEqual(loop.snapshot()['mode'],'exploring')
        for invalid in (-1,0,2**53,True,1.0):
            limits['max_model_turns'] = invalid
            with self.assertRaises(ContractError): HarnessLoop(limits)
        del limits['max_model_turns']
        limits['unexpected'] = 1
        with self.assertRaises(ContractError): HarnessLoop(limits)
        for invalid in (True, 1.0, -1, 2**53):
            with self.assertRaises(ContractError): loop.narrow_remaining(invalid,10)
            with self.assertRaises(ContractError): loop.reserve_read('content','sha256:'+'1'*64,0,invalid)


if __name__ == '__main__': unittest.main()
