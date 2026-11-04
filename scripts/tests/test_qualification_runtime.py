"""Exercise runner selection and start -> retained inspection -> export failures."""
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import qualify_interceptor_release as qualification


class DockerRuntimeTest(unittest.TestCase):
    def test_custom_context_is_frozen_and_conflicting_ambient_settings_removed(self):
        with tempfile.TemporaryDirectory() as temp, patch.dict(os.environ, {
                'DOCKER_CONTEXT': 'qualification', 'DOCKER_HOST': 'unix:///wrong.sock',
                'DOCKER_API_VERSION': '1.40', 'QUALIFICATION_DOCKER_VERSION': '29.0.1'}, clear=True):
            calls = []
            def invoke(*args):
                calls.append(args)
                if 'context' in args:
                    return 'unix:///tmp/qualification/docker.sock'
                if 'version' in args:
                    self.assertNotIn('DOCKER_API_VERSION', os.environ)
                    return json.dumps({side: {'Version': '29.0.1', 'ApiVersion': '1.52'} for side in ('Client', 'Server')})
                return 'daemon-qualification'
            with patch.object(qualification.shutil, 'which', return_value='/test/docker'), patch.object(qualification, 'run', side_effect=invoke):
                result = qualification.docker_runtime(Path(temp))
            self.assertEqual(result['endpoint'], 'unix:///tmp/qualification/docker.sock')
            self.assertEqual(calls[0][-1], 'qualification')
            self.assertTrue(all(c[1:3] == ('--host', result['endpoint']) for c in calls[1:]))
            self.assertEqual(os.environ['DOCKER_HOST'], result['endpoint'])
            self.assertNotIn('DOCKER_CONTEXT', os.environ)
            self.assertEqual(json.loads((Path(temp) / 'docker-runtime.json').read_text()), result)

    def test_both_versions_are_checked_and_remote_daemons_rejected(self):
        for side in ('Client', 'Server'):
            with self.subTest(side=side), tempfile.TemporaryDirectory() as temp, patch.dict(os.environ, {
                    'DOCKER_HOST': 'unix:///tmp/daemon.sock', 'QUALIFICATION_DOCKER_VERSION': '29.0.1'}, clear=True):
                versions = {s: {'Version': '29.0.1', 'ApiVersion': '1.52'} for s in ('Client', 'Server')}
                versions[side]['Version'] = '27.0.0'
                with patch.object(qualification.shutil, 'which', return_value='/test/docker'), patch.object(qualification, 'run', side_effect=[json.dumps(versions), 'daemon']):
                    with self.assertRaisesRegex(RuntimeError, 'both match'):
                        qualification.docker_runtime(Path(temp))
                self.assertTrue((Path(temp) / 'docker-runtime.json').exists())
        with tempfile.TemporaryDirectory() as temp, patch.dict(os.environ, {'DOCKER_HOST': 'tcp://remote:2375'}, clear=True), patch.object(qualification.shutil, 'which', return_value='/test/docker'):
            with self.assertRaisesRegex(ValueError, 'local Docker'):
                qualification.docker_runtime(Path(temp))


class CampaignFailureRetentionTest(unittest.TestCase):
    def invoke(self, mode):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        work, output = root / 'work', root / 'output'
        work.mkdir(); output.mkdir(); (work / 'run').mkdir()
        config = work / 'config.yaml'; config.write_text('fixture')
        cli = root / 'operatorctl'
        # A subprocess fixture, with inspect reading what the earlier start
        # invocation actually persisted. No paid provider or Docker access.
        cli.write_text(f'#!{sys.executable}\n' + f'root={str(work)!r}\nmode={mode!r}\n' + '''
import json, pathlib, sys
root=pathlib.Path(root)
args=sys.argv[1:]
with (root/'calls').open('a') as out:out.write(args[0]+'\\n')
if args[:2]==['campaign','start']:
    record={'campaign_id':'fixture-campaign','completion':{'code':'preparation_failed','diagnostic':{
        'stage':'local Harness image inspection','message':'Docker does not support the required platform inspection (exit 125)'}}}
    (root/'state/starts/fixture').mkdir(parents=True)
    (root/'state/starts/fixture/completion.json').write_text(json.dumps(record))
    (root/'run/start.json').write_text(json.dumps({'campaign_id':'fixture-campaign'}))
    print(json.dumps(record))
    if mode!='success' and mode!='export-only':
        print('Campaign preparation failed: Docker does not support the required platform inspection (exit 125)',file=sys.stderr)
        sys.exit(1)
elif args[0]=='inspect':
    if mode=='inspect-failure':
        print('inspection unavailable',file=sys.stderr);sys.exit(1)
    print((root/'state/starts/fixture/completion.json').read_text())
elif args[0]=='export':
    if mode!='success':
        print('report/export could not be published',file=sys.stderr);sys.exit(1)
    out=pathlib.Path(args[args.index('--output')+1]);out.mkdir()
    (out/'result-manifest.json').write_text('{"journal_intact":true}')
''')
        cli.chmod(0o700)
        return cli, config, work, output

    def test_preparation_failure_survives_export_and_inspection_failure(self):
        for mode in ('preparation', 'inspect-failure'):
            with self.subTest(mode=mode):
                cli, config, work, output = self.invoke(mode)
                with self.assertRaisesRegex(RuntimeError, 'Docker does not support') as error:
                    qualification.execute_campaign(cli, config, work, output)
                self.assertNotIn('report/export could not', str(error.exception))
                self.assertIn('report/export could not', (output / 'secondary-failures.log').read_text())
                self.assertEqual((output / 'start-records/fixture/completion.json').read_bytes(), (work / 'state/starts/fixture/completion.json').read_bytes())
                self.assertTrue((output / 'start-link.json').is_file())
                if mode == 'preparation':
                    self.assertEqual(json.loads((output / 'start-inspection.json').read_text())['completion']['code'], 'preparation_failed')
                self.assertEqual((work / 'calls').read_text().splitlines(), ['campaign', 'inspect', 'export'])

    def test_success_still_requires_successful_evidence_export(self):
        cli, config, work, output = self.invoke('success')
        self.assertEqual(qualification.execute_campaign(cli, config, work, output), {'journal_intact': True})
        cli, config, work, output = self.invoke('export-only')
        with self.assertRaisesRegex(RuntimeError, 'required evidence retention failed'):
            qualification.execute_campaign(cli, config, work, output)


if __name__ == '__main__':
    unittest.main()
