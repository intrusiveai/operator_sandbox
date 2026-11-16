import json
from pathlib import Path
import unittest
from operator_contracts import Protocol, ContractError
from operator_contracts.interface import document_identity, interface_resources
ROOT = Path(__file__).resolve().parents[3]


class InterfaceTest(unittest.TestCase):
    def test_standalone_protocol(self):
        raw = (ROOT/'schemas/asyncapi.json').read_bytes()
        expected = document_identity(raw)
        protocol = Protocol.from_interface(raw, expected)
        self.assertEqual(protocol.package_identity(), expected)
        self.assertEqual(protocol.registry_digests(), Protocol(ROOT/'schemas').registry_digests())
        for message in json.loads((ROOT/'schemas/fixtures/startup-example.json').read_bytes()):
            kind = message['kind']
            protocol.validate_control('host' if kind in ('bootstrap','initialize','admission_open','terminate') else 'guest', json.dumps(message).encode())
        with self.assertRaises(ContractError):
            Protocol.from_interface(raw + b'\n', expected)

    def test_implementation_is_not_identity(self):
        raw = (ROOT/'schemas/asyncapi.json').read_bytes()
        pin = document_identity(raw)
        p = Protocol.from_interface(raw, pin)
        files = interface_resources(raw, pin)
        profiles = {'jcs-v1':'semantics/jcs', 'manifest-paths-v1':'semantics/paths', 'harness-loop-v1':'semantics/loop'}
        files.update({path:b'Informational documentation' for path in profiles.values()})
        a, first = p.build_package_manifest(pin['package_version'], files, profiles)
        files['source/validator.py'] = b'# changed implementation'
        b, second = p.build_package_manifest(pin['package_version'], files, profiles)
        self.assertEqual(first, second)
        self.assertNotEqual(a, b)
        p.verify_package(b, files, first)
        files['operations.json'] = b'{"operations":[]}'
        with self.assertRaises(ContractError): p.verify_package(b, files, first)
