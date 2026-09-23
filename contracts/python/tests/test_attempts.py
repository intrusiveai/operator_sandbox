from dataclasses import FrozenInstanceError
import json
from pathlib import Path
import unittest

from operator_contracts import ContractError
from operator_contracts.attempts import AllocationError, AttemptAllocator, AttemptLedger, AttemptSubmission

ROOT = Path(__file__).resolve().parents[3]


class AttemptsTest(unittest.TestCase):
    def test_shared_attempt_bookkeeping(self):
        cases = json.loads((ROOT/'schemas/fixtures/attempt-bookkeeping.json').read_text())
        for case in cases:
            with self.subTest(case=case['name']):
                if case['mode'] == 'allocator':
                    allocator = AttemptAllocator(case['high_watermark'])
                    water = case['high_watermark']
                    for i, step in enumerate(case['steps']):
                        with self.subTest(step=i):
                            allocation, error = None, None
                            try:
                                allocation = allocator.allocate(step['raw'].encode(), step['request_id'], step['attempt_id'])
                            except ContractError as exc:
                                error = exc
                                if isinstance(exc, AllocationError):
                                    allocation = exc.allocation
                            self.assertEqual(error is None, step['valid'])
                            self.assertEqual(allocation.attempt_index if allocation else 0, step['allocated_index'])
                            if allocation:
                                water = step['allocated_index']
                                self.assertEqual((allocation.request_id, allocation.attempt_id), (step['request_id'], step['attempt_id']))
                            self.assertEqual(allocator.high_watermark, water)
                    continue
                self.assertEqual(case['mode'], 'ledger')
                ledger = AttemptLedger(case['high_watermark'], case['maximum'])
                for i, step in enumerate(case['steps']):
                    with self.subTest(step=i):
                        def run():
                            action = step['action']
                            if action == 'observe':
                                submission = AttemptSubmission(step['request_id'], step['attempt_id'], step['attempt_index'], step['identity_digest'])
                                record, replay = ledger.observe(submission)
                                self.assertEqual((record.submission, record.state, replay), (submission, step['state'], step['replay']))
                                self.assertEqual(ledger.lookup(step['request_id']), record)
                                with self.assertRaises(FrozenInstanceError):
                                    record.state = 'forged'
                            elif action == 'admit':
                                self.assertEqual(ledger.admit(step['request_id']), step['fresh'])
                            elif action == 'resolve':
                                ledger.resolve(step['request_id'], step['outcome'], step['result_digest'])
                                record = ledger.lookup(step['request_id'])
                                self.assertEqual((record.state, record.result_digest), (step['outcome'], step['result_digest']))
                            elif action == 'close':
                                ledger.close()
                            elif action == 'retain':
                                pass # Target restore/skip do not touch campaign bookkeeping.
                            else:
                                self.fail('unknown fixture action')
                        if step['valid']:
                            run()
                        else:
                            with self.assertRaises(ContractError):
                                run()
                        self.assertEqual((ledger.high_watermark, ledger.admissions, ledger.closed),
                                         (step['high_watermark'], step['admissions'], step['closed']))

    def test_boundaries(self):
        for seed in (-1, 2**53, True, 0.0):
            with self.assertRaises(ContractError): AttemptAllocator(seed)
            with self.assertRaises(ContractError): AttemptLedger(seed)
        for maximum in (-1, 0, 2**53, True, 1.0):
            with self.assertRaises(ContractError): AttemptLedger(0, maximum)
        allocator = AttemptAllocator()
        with self.assertRaises(ContractError): allocator.allocate(b' '*(4194304+1), 'r1', 'a1')
        self.assertEqual(allocator.high_watermark, 0)
        ledger = AttemptLedger()
        for index in (True, 1.0):
            with self.assertRaises(ContractError):
                ledger.observe(AttemptSubmission('r1', 'a1', index, 'sha256:'+'1'*64))
        self.assertEqual(ledger.high_watermark, 0)


if __name__ == '__main__': unittest.main()
