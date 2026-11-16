#!/usr/bin/env python3
"""Optional campaign regression using supplied archives and three local model replies.

Run on an otherwise idle host. The runner never publishes or calls cloud models.
This does not establish OpenAPI compatibility and is not a release gate.
"""
import argparse
from contextlib import closing
import json
import os
from pathlib import Path
import platform
import shutil
import signal
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid

from integration_qualification import digest
from qualification_model import ModelFixture


def run(*args, env=None, timeout=300):
    result = subprocess.run(list(map(str, args)), env=env, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f'{args[:3]} failed:\n{result.stdout}\n{result.stderr}')
    return result.stdout.strip()


def save(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')
    path.chmod(0o600)


def docker_runtime(output):
    """Freeze the same local daemon for fixture CLI calls and both products."""
    executable = shutil.which('docker')
    if not executable:
        raise RuntimeError('qualification requires the Docker CLI')
    context = os.environ.get('DOCKER_CONTEXT')
    endpoint = os.environ.get('DOCKER_HOST') if not context else None
    if not endpoint:
        endpoint = run(executable, 'context', 'inspect', '--format', '{{.Endpoints.docker.Host}}', *([context] if context else []))
    path = endpoint.removeprefix('unix://')
    if (not endpoint.startswith('unix:///') or str(Path(path)) != path or
            '..' in Path(path).parts or any(ord(c) < 32 for c in endpoint)):
        raise ValueError('qualification requires a local Docker Unix socket')
    # setup-docker-action uses its own socket. Operator intentionally ignores
    # ambient Docker variables, so its private config must name this socket too.
    for key in list(os.environ):
        if key.startswith('DOCKER_') and key != 'DOCKER_CONFIG':
            os.environ.pop(key)
    os.environ['DOCKER_HOST'] = endpoint
    version = json.loads(run(executable, '--host', endpoint, 'version', '--format', '{{json .}}'))
    record = {'executable': executable, 'endpoint': endpoint,
              'client': {k: version['Client'].get(k) for k in ('Version', 'ApiVersion', 'Os', 'Arch')},
              'server': {k: version['Server'].get(k) for k in ('Version', 'ApiVersion', 'Os', 'Arch')},
              'daemon_id': run(executable, '--host', endpoint, 'info', '--format', '{{.ID}}')}
    save(output / 'docker-runtime.json', record)
    print('Qualification Docker: ' + json.dumps(record), flush=True)
    expected = os.environ.get('QUALIFICATION_DOCKER_VERSION')
    if expected and any(record[side]['Version'] != expected for side in ('client', 'server')):
        raise RuntimeError('qualification Docker CLI and daemon must both match the workflow pin; see docker-runtime.json')
    return record


def execute_campaign(op, config, work, output):
    """Keep the start failure primary, even if no reportable journal exists."""
    try:
        result = subprocess.run([str(op), 'campaign', 'start', '--config', str(config), '--run', str(work / 'run')],
                                text=True, capture_output=True, timeout=300)
    except subprocess.TimeoutExpired as error:
        def text(value):
            return value.decode('utf-8', errors='replace') if isinstance(value, bytes) else value or ''
        result = subprocess.CompletedProcess(error.cmd, 124, text(error.stdout), text(error.stderr) + '\nQualification campaign deadline exceeded.')
    (output / 'operator.log').write_text(result.stdout + result.stderr)
    secondary = []
    try:
        # This works before a campaign journal exists. Preserve the original
        # start records as well as a fresh CLI inspection before temp cleanup.
        if (work / 'state/starts').exists():
            shutil.copytree(work / 'state/starts', output / 'start-records')
        if (work / 'run/start.json').exists():
            shutil.copyfile(work / 'run/start.json', output / 'start-link.json')
        save(output / 'start-inspection.json', json.loads(run(op, 'inspect', '--run', work / 'run', timeout=45)))
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired) as error:
        secondary.append('Start inspection/retention: ' + str(error))
    try:
        run(op, 'export', '--run', work / 'run', '--output', output / 'campaign', timeout=120)
    except (OSError, RuntimeError, subprocess.TimeoutExpired) as error:
        secondary.append('Campaign export: ' + str(error))
    if secondary:
        (output / 'secondary-failures.log').write_text('\n'.join(secondary) + '\n')
    if result.returncode:
        raise RuntimeError(f'Operator campaign failed (exit {result.returncode}); see operator.log and start-inspection.json.\n'
                           + (result.stderr or result.stdout)[-8192:])
    if secondary:
        raise RuntimeError('Campaign completed but required evidence retention failed; see secondary-failures.log')
    return json.loads((output / 'campaign/result-manifest.json').read_text())


def campaign(operator_archive, interceptor_archive, pin, output, runtime):
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
                    stream.write('docker: ' + json.dumps({k: runtime[k] for k in ('executable', 'endpoint')}) + '\n')
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
                facts = execute_campaign(op, config, work, output)
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
                save(output / 'checks.json', dict(checks=checks, model_failures=model.failures, cli_exit=0))
                if not all(checks.values()):
                    raise RuntimeError('controlled campaign did not pass every check; inspect retained evidence')
                succeeded = True
                return checks, metadata
        finally:
            primary = sys.exc_info()[1]
            cleanup_errors = []
            # Each cleanup step is independent. A secondary failure must neither
            # replace the campaign error nor prevent remaining owned cleanup.
            def cleanup_step(name, action):
                try:
                    action()
                except Exception as error:
                    cleanup_errors.append(name + ': ' + str(error))

            def stop_harness():
                if not succeeded and (work / 'run/start.json').exists():
                    observed = json.loads(run(op, 'inspect', '--run', work / 'run', timeout=45))
                    stopped = subprocess.run([str(op), 'campaign', 'terminate', '--config', str(work / 'config/config.yaml'),
                        '--campaign', observed['campaign_id']], capture_output=True, text=True, timeout=45)
                    (output / 'fixture-cleanup.log').write_text(stopped.stdout + stopped.stderr)

            def stop_interceptor():
                if process and process.poll() is None:
                    process.send_signal(signal.SIGINT)
                    try:
                        process.wait(timeout=45)
                    except subprocess.TimeoutExpired:
                        process.kill(); process.wait(timeout=10)

            def remove_target():
                ids = run('docker', 'ps', '-aq', '--filter', 'ancestor=' + wrapper, timeout=45).splitlines()
                if ids:
                    run('docker', 'rm', '-f', *ids, timeout=45)

            cleanup_step('Harness cleanup', stop_harness)
            cleanup_step('Interceptor shutdown', stop_interceptor)
            cleanup_step('Target cleanup', remove_target)
            for image in (wrapper, target):
                cleanup_step('Fixture image removal', lambda: subprocess.run(['docker', 'image', 'rm', image], capture_output=True, timeout=30))
            if cleanup_errors:
                diagnostic = '\n'.join(cleanup_errors) + '\n'
                print('Secondary fixture cleanup failure: ' + diagnostic, file=sys.stderr)
                try:
                    (output / 'fixture-cleanup-errors.log').write_text(diagnostic)
                except OSError:
                    pass
                if primary is None:
                    raise RuntimeError('Qualification cleanup failed; see fixture-cleanup-errors.log')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--dependencies', type=Path, required=True,
                        help='private test configuration with Interceptor source/version and Harness image/source/contract')
    parser.add_argument('--operator-archive', type=Path, required=True)
    parser.add_argument('--interceptor-archive', type=Path, required=True)
    parser.add_argument('--interceptor-sha256', required=True)
    args = parser.parse_args()
    args.output = args.output.resolve(); args.output.mkdir(mode=0o700, parents=True, exist_ok=False)
    runtime = docker_runtime(args.output)
    pin = json.loads(args.dependencies.read_text())
    if digest(args.interceptor_archive.read_bytes()) != args.interceptor_sha256:
        parser.error('supplied Interceptor archive differs from its transfer digest')
    operator, native = args.operator_archive.resolve(), args.interceptor_archive.resolve()
    run('docker', 'pull', '--platform', 'linux/' + {'x86_64': 'amd64', 'arm64': 'arm64', 'aarch64': 'arm64'}[platform.machine()], pin['harness']['image'], timeout=600)
    image_platform = 'linux/' + {'x86_64': 'amd64', 'arm64': 'arm64', 'aarch64': 'arm64'}[platform.machine()]
    # Exercise the actual required feature before creating a target session.
    run(runtime['executable'], '--host', runtime['endpoint'], 'image', 'inspect', '--platform', image_platform,
        '--format', '{{.Id}}', pin['harness']['image'])
    checks, metadata = campaign(operator, native, pin, args.output, runtime)
    result = {'schema_version': 1, 'passed': True, 'platform': platform.system().lower() + '/' + {'x86_64': 'amd64', 'arm64': 'arm64', 'aarch64': 'arm64'}[platform.machine()],
        'source_commit': metadata['source_commit'], 'test_revision': run('git', 'rev-parse', 'HEAD'), 'dependencies': pin,
        'contract': pin['harness']['contract'], 'interceptor_contract_version': pin['interceptor_contract_version'],
        'operator_archive_sha256': digest(operator.read_bytes()), 'interceptor_archive_sha256': digest(native.read_bytes()), 'checks': checks,
        'test_worktree_modified': bool(run('git', 'status', '--porcelain')),
        'workflow_run_id': int(os.environ.get('GITHUB_RUN_ID', '0')), 'workflow_run_attempt': int(os.environ.get('GITHUB_RUN_ATTEMPT', '0')),
        'local_verification': True, 'establishes_contract_compatibility': False}
    save(args.output / 'result.json', result)


if __name__ == '__main__':
    main()
