import base64
import json
from pathlib import Path
import unittest
from operator_contracts import Protocol, ContractError

ROOT = Path(__file__).resolve().parents[3]


class PackageTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.protocol = Protocol(ROOT / 'schemas')

    def test_shared_package_integrity(self):
        cases = json.loads((ROOT / 'schemas/fixtures/package-integrity.json').read_text())
        for case in cases:
            with self.subTest(case=case['name']):
                manifest = base64.b64decode(case['manifest_base64'], validate=True)
                files = {name: base64.b64decode(raw, validate=True) for name, raw in case.get('files_base64', {}).items()}
                def run():
                    if case['mode'] == 'manifest': return self.protocol.validate_package_manifest(manifest)
                    if case['mode'] == 'verify': return self.protocol.verify_package(manifest, files, case['expected'])
                    if case['mode'] == 'build':
                        built, pin = self.protocol.build_package_manifest(case['expected']['package_version'], files, case['profiles'])
                        self.assertEqual(built, base64.b64decode(case['canonical_base64'], validate=True))
                        self.assertEqual(pin, case['expected'])
                        return
                    self.fail('unknown fixture mode')
                if case['valid']: run()
                else:
                    with self.assertRaises(ContractError): run()

    def payload(self):
        files = {path.name: path.read_bytes() for path in (ROOT / 'schemas').glob('*.schema.json')}
        for name in ('catalog.json', 'operations.json'): files[name] = (ROOT / 'schemas' / name).read_bytes()
        profiles = {'jcs-v1':'semantics/digests.md', 'manifest-paths-v1':'semantics/paths.md', 'harness-loop-v1':'semantics/limits.md'}
        files.update({name:b'Fixture semantic profile, not a published release.\n' for name in profiles.values()})
        return files, profiles

    def test_verified_loader(self):
        p = self.protocol
        self.assertIsNone(p.package_identity())
        files, profiles = self.payload()
        manifest, pin = p.build_package_manifest('0.0.0', files, profiles)
        loaded = p.load_verified_protocol(manifest, files, pin)
        self.assertEqual(loaded.package_identity(), pin)
        self.assertEqual(loaded.registry_digests(), p.registry_digests())
        message = json.dumps(json.loads((ROOT / 'schemas/fixtures/startup-example.json').read_text())[0]).encode()
        loaded.validate_control('host', message)
        c = json.loads((ROOT / 'schemas/fixtures/identity-validation.json').read_text())[0]
        dec = lambda key: base64.b64decode(c[key], validate=True)
        args = ([json.dumps(m).encode() for m in c['messages']], dec('tree_base64'), dec('skill_set_base64'),
                [base64.b64decode(raw, validate=True) for raw in c['skills_base64']], dec('context_base64'), dec('bundle_base64'), dec('prompt_base64'))
        p.validate_launch_identities(*args)
        with self.assertRaises(ContractError): loaded.validate_launch_identities(*args)
        loaded.package_identity()['package_version'] = 'changed'
        files['catalog.json'] = b'changed'
        self.assertEqual(loaded.package_identity(), pin)
        loaded.validate_control('host', message)
        with self.assertRaises(ContractError): p.verify_package(manifest, files, pin)

    def test_loading_stays_offline(self):
        files, profiles = self.payload()
        files['wire-common.schema.json'] = b'{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"urn:operator:schema:wire-common:v1alpha1","$ref":"https://invalid.example/schema"}'
        manifest, pin = self.protocol.build_package_manifest('0.0.0', files, profiles)
        with self.assertRaises(ContractError): self.protocol.load_verified_protocol(manifest, files, pin)
        files, profiles = self.payload()
        files['../escape'] = b'x'
        with self.assertRaises(ContractError): self.protocol.build_package_manifest('0.0.0', files, profiles)

    def test_builder_rejects_invalid_source_values(self):
        files, profiles = self.payload()
        with self.assertRaises(ContractError):
            self.protocol.build_package_manifest('\ud800', files, profiles)
        with self.assertRaises(ContractError):
            self.protocol.build_package_manifest('0.0.0', files, {**profiles, '\ud800': 'semantics/paths.md'})
        files['catalog.json'] = bytearray(files['catalog.json'])
        with self.assertRaises(ContractError):
            self.protocol.build_package_manifest('0.0.0', files, profiles)


if __name__ == '__main__': unittest.main()
