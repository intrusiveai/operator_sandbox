#!/usr/bin/env python3
"""Qualify downloaded release archives using Docker and three local model replies.

Run on an otherwise idle host. The runner never publishes or calls cloud models.
The --local option labels development evidence, which promotion cannot accept.
"""
import argparse
from contextlib import closing
import json
import os
from pathlib import Path
import platform
import signal
import socket
import subprocess
import tarfile
import tempfile
import time
import uuid

from integration_qualification import digest, validate_pin
from qualification_model import ModelFixture
from publish_host_release import GitHub, asset_stem, verify_inventory, verify_uploaded, MARKER


def run(*args, env=None, timeout=300):
    result = subprocess.run(list(map(str, args)), env=env, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f'{args[:3]} failed:\n{result.stdout}\n{result.stderr}')
    return result.stdout.strip()


def save(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')
    path.chmod(0o600)


def download(url, path, expected):
    run('curl', '--fail', '--location', '--proto', '=https', '--proto-redir', '=https', '--max-time', '300', '--output', path, url)
    if digest(path.read_bytes()) != expected:
        raise ValueError('downloaded archive checksum differs')


def campaign(operator_archive, interceptor_archive, pin, output):
    with closing(socket.socket()) as check:
        check.bind(('127.0.0.1', 8080))  # Never displace another Interceptor.
    suffix = uuid.uuid4().hex
    target, wrapper = 'operator-qualification-target:' + suffix, 'operator-qualification-wrapper:' + suffix
    process = None
    succeeded = False
    with tempfile.TemporaryDirectory(prefix='operator-integration-') as temp:
        work = Path(temp).resolve()
        env = dict(os.environ, INTERCEPTOR_DATA_DIR=str(work / 'native-state'), QUALIFICATION_UNUSED_KEY='fixture-unused')
        for archive, name in ((operator_archive, 'operator'), (interceptor_archive, 'interceptor')):
            with tarfile.open(archive) as source:
                source.extractall(work / name, filter='data')
        op = work / 'operator/operator_sandbox/bin/operatorctl'
        native = work / 'interceptor/interceptor_sandbox/interceptor'
        run(op, 'release', 'check', '--directory', op.parent.parent)
        info = json.loads(run(native, 'version', '--json'))
        if info['source_commit'] != pin['source_commit'] or info['interceptor_contract_version'] != pin['interceptor_contract_version']:
            raise ValueError('Interceptor executable differs from dependency pin')
        save(output / 'native-version.json', info)
        install = json.loads(run(op, 'install', '--archive', operator_archive, '--root', work / 'installation',
            '--config', work / 'config/config.yaml', '--state-root', work / 'state', '--image', pin['harness']['image']))
        op = Path(install['command'])
        metadata = json.loads(run(op, 'version'))
        if metadata['interceptor_contract_version'] != pin['interceptor_contract_version'] or metadata['contract_digest'] != pin['harness']['contract']['package_digest']:
            raise ValueError('Operator executable differs from dependency contracts')
        save(output / 'operator-version.json', metadata)
        # The local target has no model integration, tools, or network dependency.
        context = work / 'target'; context.mkdir()
        (context / 'app.py').write_text('''from http.server import BaseHTTPRequestHandler, HTTPServer
class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length", "0")))
        raw=b'{"answer":"QA_CONFIG_OK"}'
        self.send_response(200); self.send_header("Content-Type","application/json")
        self.send_header("Content-Length",str(len(raw))); self.end_headers(); self.wfile.write(raw)
HTTPServer(("127.0.0.1",8080),Handler).serve_forever()
''')
        (context / 'Dockerfile').write_text('FROM python:3.12-slim\nCOPY app.py /app/app.py\nWORKDIR /app\nCMD ["python3","-B","/app/app.py"]\n')
        try:
            run('docker', 'build', '--tag', target, context, timeout=600)
            image = run('docker', 'image', 'inspect', '--format', '{{.Id}}', target)
            save(output / 'target-image.json', {'image_id': image, 'controlled_fixture': True})
            blueprint = work / 'environment'; (blueprint / 'assets').mkdir(parents=True)
            save(blueprint / 'environment.yaml', {'api_version': 'interceptor.dev/v1alpha2', 'kind': 'Environment',
                'metadata': {'name': 'operator-qualification', 'version': '0.1.0', 'reviewed_by': 'controlled-fixture', 'reviewed_at': '2026-11-03T00:00:00Z'},
                'application': {'image': target + '@' + image, 'launch': {'command': ['python3', '-B', '/app/app.py'], 'workdir': '/app', 'environment': {'MODEL_BASE_URL': 'http://127.0.0.1:18080/v1'}},
                    'interface': {'kind': 'http', 'operations': [{'id': 'invoke', 'method': 'POST', 'path': '/invoke', 'delivery_asset': 'assets/invoke.json'}],
                        'http': {'listen': 'http://127.0.0.1:8080'}, 'startup_timeout_ms': 30000, 'turn_timeout_ms': 60000},
                    'persistence': {'snapshot_capable': False, 'mutable_roots': ['/state/app'], 'quiesce': {'kind': 'none'}}}})
            save(blueprint / 'assets/invoke.json', {'description': 'Return a controlled marker',
                'input': {'media_type': 'application/json', 'encoding': 'json', 'max_bytes': 65536, 'basis': 'synthetic',
                    'schema': {'type': 'object', 'properties': {'query': {'type': 'string'}}, 'required': ['query'], 'additionalProperties': False}},
                'output': {'media_type': 'application/json', 'encoding': 'json', 'max_bytes': 65536, 'basis': 'unknown'}})
            run(native, 'environment', 'validate', blueprint, env=env)
            run(native, 'environment', 'seal', blueprint, env=env)
            run(native, 'app', 'build', '--environment', blueprint, '--tag', wrapper, '--json', env=env, timeout=600)
            run(native, 'app', 'verify', '--environment', blueprint, env=env)
            save(work / 'native.json', json.loads(run(native, 'capabilities', '--environment', blueprint, env=env)))
            run('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', work / 'tls.key', '-out', work / 'tls.pem',
                '-days', '1', '-subj', '/CN=localhost', '-addext', 'subjectAltName=IP:127.0.0.1')
            with ModelFixture(work / 'tls.pem', work / 'tls.key') as model, (output / 'interceptor.log').open('w') as log:
                process = subprocess.Popen([str(native), 'run', '--environment', str(blueprint), '--model-provider', 'openai-chat',
                    '--provider-auth', 'api-key-env', '--model-api-key-env', 'QUALIFICATION_UNUSED_KEY', '--allow-model', 'unused-model',
                    '--provider-base-url', 'http://127.0.0.1:1/v1', '--feedback-profile', 'black-box'], env=env, stdout=log, stderr=subprocess.STDOUT)
                deadline = time.monotonic() + 120
                while 'Interceptor local API:' not in (output / 'interceptor.log').read_text():
                    if process.poll() is not None or time.monotonic() > deadline:
                        raise RuntimeError('Interceptor did not become ready; see interceptor.log')
                    time.sleep(.2)
                config = work / 'config/config.yaml'
                target_profile = work / 'target.json'
                save(target_profile, {'api_version': 'operator.dev/target-profile/v1alpha1', 'id': 'local', 'target_id': 'qualification',
                    'adapter': 'interceptor/v1', 'allow_target_stop': True, 'scopes': {'operation_ids': ['invoke'], 'caller_principal_ids': [], 'routes': [], 'allow_retained_injections': False},
                    'feedback': {'ceiling': 'black-box', 'allowed_kinds': ['target_output'], 'max_attempt_bytes': 1048576}, 'operation_timeout_ms': 90000})
                save(work / 'model.json', {'api_version': 'operator.dev/model-provider-profile/v1alpha1', 'id': 'controlled', 'provider': 'litellm',
                    'codec_id': 'openai-chat-text-tools-v1', 'model': 'test-model', 'endpoint': model.url, 'authentication': 'none',
                    'maximum_prompt_tokens': 100000, 'maximum_completion_tokens': 1024, 'maximum_response_bytes': 1048576,
                    'codec_options': {'instruction_role': 'developer', 'response_models': ['test-model']}, 'ca_certificates_pem': (work / 'tls.pem').read_text()})
                with config.open('a') as stream:
                    stream.write(f'\ntarget: {{profile_file: {json.dumps(str(target_profile))}}}\nmodel: {{profile_file: {json.dumps(str(work / "model.json"))}}}\n')
                run(op, 'capabilities', 'export', '--config', config, '--native', work / 'native.json', '--output', work / 'capabilities.json', '--target-profile', target_profile)
                public = json.loads((work / 'capabilities.json').read_text())
                bundle = json.loads((Path(__file__).resolve().parents[1] / 'schemas/fixtures/scenario-bundle-scenarios.json').read_text())
                bundle['target_requirements'] = {'target_id': 'qualification', 'capability_source_digest': public['source']['capability_source_digest']}
                bundle['objectives'][0]['description'] = 'Verify the controlled target returns its marker through permitted observation bytes.'
                bundle['scenarios'][0]['hypothesis'] = 'The direct invocation returns QA_CONFIG_OK.'
                bundle['scenarios'][0]['guidance']['surfaces'] = ['Direct application input.']
                bundle['scenarios'][0]['required'] = True
                save(work / 'bundle.json', bundle)
                run(op, 'submit', '--config', config, '--bundle', work / 'bundle.json', '--capabilities', work / 'capabilities.json', '--output', work / 'run')
                result = subprocess.run([str(op), 'campaign', 'start', '--config', str(config), '--run', str(work / 'run')], text=True, capture_output=True, timeout=300)
                (output / 'operator.log').write_text(result.stdout + result.stderr)
                # Export even if execution failed, using a fresh CLI invocation.
                run(op, 'export', '--run', work / 'run', '--output', output / 'campaign')
                facts = json.loads((output / 'campaign/result-manifest.json').read_text())
                exit_info = facts.get('harness_exit') or {}
                events = [json.loads(line) for path in (output / 'campaign').rglob('*.jsonl') for line in path.read_text().splitlines()]
                checked = [e for e in events if e.get('kind') == 'interceptor.compatibility']
                attempts = facts['attempts']
                checks = {'compatibility': any(e.get('metadata', {}).get('actual') == pin['interceptor_contract_version'] for e in checked),
                    'attachment': facts['target_adapter'] == 'interceptor/v1' and len(attempts) == 1,
                    'invocation': len(attempts) == 1 and attempts[0]['dispatched'] and attempts[0]['invocation_state'] == 'succeeded' and attempts[0]['scenario_id'] == 'scenario-marker',
                    'observation_read': model.observed and model.turns == 3 and not model.failures,
                    'finish_accepted': exit_info.get('finish_accepted') is True and exit_info.get('assessment_committed') is True,
                    'exit_zero': exit_info.get('exit_observed') is True and exit_info.get('exit_code') == 0 and not exit_info.get('forced_termination'),
                    'cleanup': exit_info.get('cleanup_complete') is True and facts['target_stop'] == 'confirmed',
                    'export': facts['journal_intact'] is True and (output / 'campaign/report.md').is_file()}
                save(output / 'checks.json', dict(checks=checks, model_failures=model.failures, cli_exit=result.returncode))
                if result.returncode or not all(checks.values()):
                    raise RuntimeError('controlled campaign did not pass every check; inspect retained evidence')
                succeeded = True
                return checks, metadata
        finally:
            # A timed-out host subprocess may have left an owned Harness. Use
            # Operator's normal exact-identity termination path before discarding
            # the private state; never kill containers by a shared image tag.
            if not succeeded and (work / 'run/start.json').exists():
                try:
                    stopped = subprocess.run([str(op), 'campaign', 'terminate', '--config', str(work / 'config/config.yaml'),
                        '--campaign', json.loads(run(op, 'inspect', '--run', work / 'run'))['campaign_id']],
                        capture_output=True, text=True, timeout=45)
                    (output / 'fixture-cleanup.log').write_text(stopped.stdout + stopped.stderr)
                except (OSError, ValueError, KeyError, RuntimeError, subprocess.TimeoutExpired):
                    # Incomplete exports still retain the state for inspection.
                    import shutil
                    if (work / 'state').exists():
                        shutil.copytree(work / 'state', output / 'retained-state', dirs_exist_ok=True)
            if process and process.poll() is None:
                process.send_signal(signal.SIGINT)
                try:
                    process.wait(timeout=45)
                except subprocess.TimeoutExpired:
                    process.kill(); process.wait()
            ids = run('docker', 'ps', '-aq', '--filter', 'ancestor=' + wrapper).splitlines()
            if ids:
                run('docker', 'rm', '-f', *ids)
            for image in (wrapper, target):
                subprocess.run(['docker', 'image', 'rm', image], capture_output=True, timeout=60)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--version')
    parser.add_argument('--source')
    parser.add_argument('--local', action='store_true')
    parser.add_argument('--operator-archive', type=Path)
    parser.add_argument('--interceptor-archive', type=Path)
    parser.add_argument('--interceptor-sha256')
    args = parser.parse_args()
    args.output = args.output.resolve(); args.output.mkdir(mode=0o700, parents=True, exist_ok=False)
    pin = json.loads((Path(__file__).resolve().parents[1] / 'release/interceptor-qualification.json').read_text())
    with tempfile.TemporaryDirectory(prefix='operator-qualification-download-') as temp:
        work = Path(temp)
        if args.local:
            if not args.operator_archive or not args.interceptor_archive or digest(args.interceptor_archive.read_bytes()) != args.interceptor_sha256:
                parser.error('local verification requires both archives and the independently verified Interceptor checksum')
            operator, native = args.operator_archive.resolve(), args.interceptor_archive.resolve()
        else:
            if platform.system() != 'Linux' or platform.machine() != 'x86_64' or not args.version or not args.source:
                parser.error('published qualification requires native Linux AMD64, version and source')
            github = GitHub('intrusiveai/operator_sandbox')
            release = github.release('v' + args.version)
            if not release or release['draft'] or not release['body'].startswith(MARKER + '\n'):
                raise ValueError('expected published managed release bytes')
            record = json.loads(release['body'].removeprefix(MARKER + '\n'))
            raw = github.download_inventory('v' + args.version)
            inventory = verify_inventory(raw, record); verify_uploaded(github, release, record['assets'])
            if inventory['source_commit'] != args.source or inventory['qualification_dependencies'] != pin:
                raise ValueError('published candidate differs from checkout or dependency pin')
            validate_pin(pin, inventory['contract'], inventory['interceptor_contract_version'])
            operator, native = work / 'operator.tar.gz', work / 'interceptor.tar.gz'
            name = asset_stem(args.version, 'linux/amd64') + '.tar.gz'
            download(f'https://github.com/{github.repository}/releases/download/v{args.version}/{name}', operator, inventory['assets'][name])
            download(pin['archive_url'], native, pin['archive_sha256'])
        run('docker', 'pull', '--platform', 'linux/' + {'x86_64': 'amd64', 'arm64': 'arm64', 'aarch64': 'arm64'}[platform.machine()], pin['harness']['image'], timeout=600)
        checks, metadata = campaign(operator, native, pin, args.output)
        result = {'schema_version': 1, 'passed': True, 'platform': platform.system().lower() + '/' + {'x86_64': 'amd64', 'arm64': 'arm64', 'aarch64': 'arm64'}[platform.machine()],
            'source_commit': metadata['source_commit'], 'test_revision': run('git', 'rev-parse', 'HEAD'), 'dependencies': pin,
            'contract': pin['harness']['contract'], 'interceptor_contract_version': pin['interceptor_contract_version'],
            'operator_archive_sha256': digest(operator.read_bytes()), 'interceptor_archive_sha256': digest(native.read_bytes()), 'checks': checks,
            'test_worktree_modified': bool(run('git', 'status', '--porcelain')),
            'workflow_run_id': int(os.environ.get('GITHUB_RUN_ID', '0')), 'workflow_run_attempt': int(os.environ.get('GITHUB_RUN_ATTEMPT', '0')),
            'inventory_digest': digest(raw) if not args.local else None, 'local_verification': args.local}
        save(args.output / 'result.json', result)


if __name__ == '__main__':
    main()
