import base64
import copy
import io
import json
from pathlib import Path
import sys
import unittest
import zipfile
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
import integration_qualification as q
from publish_host_release import check_candidates, PLATFORMS
from verify_host_contract import validate_report
import test_candidate_workflow as workflow


class QualificationTest(unittest.TestCase):
    def setUp(self):
        self.pin = json.loads((ROOT / 'release/interceptor-qualification.json').read_text())
        self.inventory = {'version': '0.1.4', 'source_commit': 'a' * 40, 'qualification_dependencies': self.pin,
                          'contract': self.pin['harness']['contract'], 'interceptor_contract_version': '0.1.0',
                          'assets': {'operator_sandbox-v0.1.4-linux_amd64.tar.gz': 'sha256:' + 'b' * 64}}
        self.run = {'id': 123, 'run_attempt': 1, 'head_sha': 'a' * 40, 'head_branch': 'candidate-v0.1.4',
                    'event': 'push', 'path': q.WORKFLOW, 'status': 'completed', 'conclusion': 'success',
                    'head_repository': {'full_name': 'intrusiveai/operator_sandbox'}}
        self.result = {'schema_version': 1, 'passed': True, 'platform': 'linux/amd64', 'source_commit': 'a' * 40,
                       'test_revision': 'a' * 40, 'inventory_digest': 'sha256:' + 'c' * 64, 'dependencies': self.pin,
                       'contract': self.inventory['contract'], 'interceptor_contract_version': '0.1.0',
                       'operator_archive_sha256': 'sha256:' + 'b' * 64, 'workflow_run_id': 123, 'workflow_run_attempt': 1,
                       'checks': {name: True for name in q.CHECKS}}

    def test_exact_results_and_dependency_binding(self):
        q.validate_pin(self.pin, self.inventory['contract'], '0.1.0')
        q.validate_result(self.result, self.inventory, 'sha256:' + 'c' * 64, self.run)
        for key in self.result:
            bad = copy.deepcopy(self.result); bad[key] = None
            with self.subTest(key=key), self.assertRaises(ValueError):
                q.validate_result(bad, self.inventory, 'sha256:' + 'c' * 64, self.run)
        changed = copy.deepcopy(self.inventory); changed['qualification_dependencies']['archive_sha256'] = 'sha256:' + 'd' * 64
        with self.assertRaises(ValueError):
            q.validate_result(self.result, changed, 'sha256:' + 'c' * 64, self.run)
        for key, value in [('archive_url', 'https://untrusted.example/archive'), ('archive_sha256', 'unknown'), ('interceptor_contract_version', '0.2.0')]:
            bad = dict(self.pin, **{key: value})
            with self.assertRaises(ValueError):
                q.validate_pin(bad, self.inventory['contract'], '0.1.0')

    def test_trust_and_bounded_archive(self):
        self.assertTrue(q.trusted_run(self.run, self.inventory, 'intrusiveai/operator_sandbox'))
        for key, value in [('event', 'pull_request'), ('path', 'arbitrary.yml'), ('conclusion', 'skipped'), ('conclusion', 'failure'),
                           ('conclusion', 'cancelled'), ('status', 'in_progress'), ('head_sha', 'f' * 40), ('head_repository', {'full_name': 'someone/fork'})]:
            with self.subTest(key=key, value=value):
                self.assertFalse(q.trusted_run(dict(self.run, **{key: value}), self.inventory, 'intrusiveai/operator_sandbox'))
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, 'w') as archive:
            archive.writestr('result.json', json.dumps(self.result))
        raw = buffer.getvalue()
        self.assertEqual(q.unpack_result(raw, {'digest': q.digest(raw)}), self.result)
        with self.assertRaises(ValueError):
            q.unpack_result(raw, {'digest': 'sha256:' + '0' * 64})

    def test_real_gate_requires_successful_job_unexpired_artifact_and_matching_result(self):
        owner = self
        class GitHub:
            repository = 'intrusiveai/operator_sandbox'
            prefix = 'repos/' + repository + '/'
            jobs = [{'name': 'qualify-interceptor', 'conclusion': 'success'}]
            artifacts = []
            def api(self, path):
                if path.startswith('contents/'):
                    return {'content': base64.b64encode(json.dumps(owner.pin).encode()).decode()}
                if '/jobs?' in path:
                    return {'jobs': self.jobs}
                if '/artifacts?' in path:
                    return {'artifacts': self.artifacts}
                return {'workflow_runs': [owner.run]}
        github = GitHub()
        with self.assertRaises(ValueError):
            q.require_qualification(github, self.inventory, self.result['inventory_digest'])
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, 'w') as archive:
            archive.writestr('result.json', json.dumps(self.result))
        raw = buffer.getvalue()
        github.artifacts = [{'id': 456, 'name': q.ARTIFACT, 'expired': False, 'digest': q.digest(raw), 'workflow_run': {'head_sha': 'a' * 40}}]
        with patch('integration_qualification.subprocess.run', side_effect=lambda *a, **kw: kw['stdout'].write(raw)):
            accepted = q.require_qualification(github, self.inventory, self.result['inventory_digest'])
            self.assertEqual(accepted['run_id'], 123)
            for field, value in [('expired', True), ('workflow_run', {'head_sha': 'b' * 40})]:
                prior = github.artifacts[0][field]; github.artifacts[0][field] = value
                with self.assertRaises(ValueError):
                    q.require_qualification(github, self.inventory, self.result['inventory_digest'])
                github.artifacts[0][field] = prior
            github.jobs[0]['conclusion'] = 'skipped'
            with self.assertRaises(ValueError):
                q.require_qualification(github, self.inventory, self.result['inventory_digest'])

    def test_archived_identity_mismatch_and_platform_pins(self):
        host = {'platform': 'linux/amd64', 'version': '0.1.4', 'source_commit': 'a' * 40,
                'interceptor_contract_version': '0.1.0', 'contract': self.inventory['contract'], 'files': [{'path': 'bin/operatorctl', 'digest': 'sha256:' + 'e' * 64}]}
        hosts = [dict(host, platform=p) for p in PLATFORMS]
        self.assertEqual(check_candidates(hosts), '0.1.4')
        for value in (None, '0.2.0', 'v0.1.0', '01.0.0', ''):
            with self.subTest(value=value), self.assertRaises(ValueError):
                check_candidates([dict(h, interceptor_contract_version=value) if i == 0 else h for i, h in enumerate(hosts)])
        report = {'platform': host['platform'], 'version': host['version'], 'source_commit': host['source_commit'], 'interceptor_contract_version': '0.1.0',
                  'contract_version': host['contract']['package_version'], 'contract_digest': host['contract']['package_digest'],
                  'archive_sha256': 'sha256:' + 'b' * 64, 'binary_sha256': 'sha256:' + 'e' * 64, 'checks': ['archive-integrity', 'native-version']}
        validate_report(report, host, report['archive_sha256'])
        for key in report:
            with self.subTest(key=key), self.assertRaises(ValueError):
                validate_report(dict(report, **{key: None}), host, report['archive_sha256'])


class PromotionGateTest(unittest.TestCase):
    def test_missing_qualification_leaves_prerelease_unchanged(self):
        case = workflow.CandidateWorkflowTest()
        case.setUp()
        self.addCleanup(case.doCleanups)
        case.github.head = workflow.SECOND
        case.publish(workflow.SECOND)
        case.qualification.side_effect = ValueError('qualification missing')
        with self.assertRaises(ValueError):
            workflow.release.promote(case.github, 42)
        self.assertTrue(case.github.current['prerelease'])


if __name__ == '__main__':
    unittest.main()
