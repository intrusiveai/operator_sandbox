"""Approval is explicit, byte-exact, and failure cannot manufacture a new pin."""
import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
import interceptor_contract as contract


class ContractApprovalTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.raw = (ROOT / contract.DOCUMENT).read_bytes()
        self.source = json.loads((ROOT / contract.SOURCE).read_text())
        for path in (contract.DOCUMENT, contract.PIN, contract.SOURCE):
            target = self.root / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes((ROOT / path).read_bytes())

    def test_adopt_compares_before_writes_and_preserves_exact_bytes(self):
        changed = self.raw + b'\n'
        previous = {p: (self.root / p).read_bytes() for p in (contract.PIN, contract.DOCUMENT, contract.SOURCE)}
        with patch.object(contract, 'compare', side_effect=subprocess.CalledProcessError(1, ['go','test'])):
            with self.assertRaises(subprocess.CalledProcessError):
                contract.adopt(self.source, changed, self.root)
        self.assertEqual(previous, {p: (self.root / p).read_bytes() for p in previous})
        with patch.object(contract, 'compare') as compare:
            pin = contract.adopt(self.source, changed, self.root)
            compare.assert_called_once_with(changed, self.root)
        self.assertEqual((self.root / contract.DOCUMENT).read_bytes(), changed)
        self.assertNotEqual(pin, contract.identity(self.raw))
        self.assertEqual(json.loads((self.root / contract.PIN).read_text()), pin)

    def test_verify_never_follows_moving_ref_or_rewrites_approval(self):
        before = (self.root / contract.PIN).read_bytes()
        with patch.object(contract, 'fetch', return_value=self.raw) as fetch, patch.object(contract, 'compare'):
            contract.verify(self.root, remote=True)
            fetch.assert_called_once_with(self.source)
        with patch.object(contract, 'fetch', return_value=self.raw + b'\n'), patch.object(contract, 'compare') as compare:
            with self.assertRaisesRegex(ValueError, 'remote contract bytes'):
                contract.verify(self.root, remote=True)
            compare.assert_not_called()
        self.assertEqual((self.root / contract.PIN).read_bytes(), before)

    def test_partial_activation_and_unknown_or_prefixed_identity_fail(self):
        (self.root / contract.DOCUMENT).write_bytes(self.raw + b' ')
        with patch.object(contract, 'compare') as compare:
            with self.assertRaisesRegex(ValueError, 'approved contract identity'):
                contract.verify(self.root)
            compare.assert_not_called()
        pin = contract.identity(self.raw)
        for bad in (dict(pin, archive_sha256='a'*64), dict(pin, interceptor_contract_sha256='sha256:'+pin['interceptor_contract_sha256'])):
            with self.assertRaises(ValueError):
                contract.validate_pin(bad)

    def test_download_binds_exact_resolved_commit(self):
        import base64
        response = {'size':len(self.raw),'encoding':'base64','content':base64.b64encode(self.raw).decode()}
        with patch.object(contract, 'api', return_value=response) as api:
            self.assertEqual(contract.fetch(self.source), self.raw)
            self.assertEqual(api.call_args.args, ('contents/'+contract.REMOTE_PATH+'?ref='+self.source['commit'],))
        with patch.object(contract, 'api', return_value={'sha':'b'*40}):
            selected=contract.select('candidate-v0.1.4')
        self.assertEqual(selected['commit'], 'b'*40)
        bad=copy.deepcopy(self.source);bad['repository']='other/repository'
        with self.assertRaises(ValueError):contract.validate_source(bad)

    def test_comparison_cannot_be_replaced_by_campaign_execution(self):
        with patch.object(contract.subprocess,'run') as run:
            contract.compare(self.raw,self.root)
        command=run.call_args.args[0]
        self.assertEqual(command[:2], ['go','test'])
        self.assertIn('^TestInterceptorContract',command)
        self.assertIn('./internal/attemptadapter',command)
        self.assertNotIn('./internal/preparation',command)
        workflow=(ROOT/'.github/workflows/verify-release.yml').read_text()
        self.assertIn('needs: [context, verify, interceptor-contract]',workflow)
        self.assertNotIn('qualify-interceptor:',workflow)
        self.assertNotIn('setup-docker-action',workflow)

if __name__=='__main__':unittest.main()
