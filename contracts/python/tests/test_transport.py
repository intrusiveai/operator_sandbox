import base64
import json
from pathlib import Path
import unittest
from operator_contracts import Protocol, ContractError
from operator_contracts.transport import spool_message_name, parse_spool_message_name

ROOT=Path(__file__).resolve().parents[3]


class TransportTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.protocol=Protocol(ROOT/'schemas')
        cls.cases=json.loads((ROOT/'schemas/fixtures/transport-codec.json').read_text())

    def test_shared_transport(self):
        p=self.protocol
        for case in self.cases:
            with self.subTest(case=case['name']):
                dec=lambda raw:base64.b64decode(raw,validate=True)
                if case['mode']=='state':
                    state=p.new_transport_state(case['role'],case['campaign_id'],case['launch_id'])
                    for index,step in enumerate(case['steps']):
                        with self.subTest(step=index):
                            def run():
                                action=step['action']
                                if action=='close':return state.close()
                                if action=='accept':return state.accept(step['lane'],dec(step['raw_base64']))
                                if action=='publish':return state.record_published(step['lane'],dec(step['raw_base64']))
                                if action=='ack':return state.apply_ack(dec(step['raw_base64']))
                                if action=='acknowledged':self.assertEqual(state.acknowledged(),step['expected']);return
                                if action=='consumed':
                                    ack=p.validate_ack(state.ack_bytes())
                                    self.assertEqual(ack['launch_id'],case['launch_id'])
                                    self.assertEqual({k:ack[k] for k in ('ordinary_seq','control_seq')},step['expected']);return
                                self.fail('unknown action')
                            if step['valid']:run()
                            else:
                                with self.assertRaises(ContractError):run()
                    continue
                frames=[]
                decoder=p.new_frame_decoder(case['lane']) if case['mode']=='frames' else None
                def run():
                    if case['mode']=='frames':
                        for encoded in case['chunks_base64']:
                            remaining=dec(encoded)
                            while True:
                                consumed,frame=decoder.feed(remaining)
                                self.assertGreaterEqual(consumed,0);self.assertLessEqual(consumed,len(remaining))
                                if frame is not None:frames.append(frame)
                                if remaining:self.assertGreater(consumed,0)
                                remaining=remaining[consumed:]
                                if not remaining:break
                        if case['eof']:decoder.end()
                        return
                    if case['mode']=='name':
                        sequence,temporary=parse_spool_message_name(case['filename'])
                        self.assertEqual((sequence,temporary),(case['sequence'],case['temporary']))
                        self.assertEqual(spool_message_name(sequence,temporary),case['filename']);return
                    if case['mode']=='spool':
                        return p.validate_spool_message(case['lane'],case['filename'],dec(case['raw_base64']))
                    self.fail('unknown fixture mode')
                if case['valid']:run()
                else:
                    with self.assertRaises(ContractError):run()
                if decoder is not None:
                    self.assertEqual(frames,[dec(raw) for raw in case['frames_base64']])
                    for raw in frames:self.assertEqual(p.encode_frame(case['lane'],raw),len(raw).to_bytes(4,'big')+raw)
                    if not case['valid']:
                        with self.assertRaises(ContractError):decoder.feed(b'\0')

    def test_boundaries(self):
        p=self.protocol
        raw=base64.b64decode(self.cases[0]['frames_base64'][0])
        wire=p.encode_frame('ordinary-out',raw)
        for split in range(len(wire)+1):
            decoder=p.new_frame_decoder('ordinary-out')
            n,first=decoder.feed(wire[:split]);self.assertEqual(n,split)
            n,second=decoder.feed(wire[split:]);self.assertEqual(n,len(wire)-split)
            self.assertEqual((first or b'')+(second or b''),raw)
        for lane,maximum in [('ordinary-out',4<<20),('control-in',64<<10)]:
            decoder=p.new_frame_decoder(lane)
            with self.assertRaises(ContractError):decoder.feed((maximum+1).to_bytes(4,'big'))
            self.assertEqual(len(decoder._body),0)
        state=p.new_transport_state('host','campaign-1','launch-1')
        state._received[0]=2**53-2 # Test-only seed; no public reset API.
        message=json.loads(raw);message['seq']=2**53-1
        state.accept('ordinary-out',json.dumps(message).encode())
        with self.assertRaises(ContractError):state.accept('ordinary-out',raw)
        for sequence in (-1,2**53,True):
            with self.assertRaises(ContractError):spool_message_name(sequence)
        with self.assertRaises(ContractError):p.new_frame_decoder('unknown')


if __name__=='__main__':unittest.main()
