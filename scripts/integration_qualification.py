"""Dependency validation and GitHub-backed admission of candidate qualification.

The JSON report is evidence only when it comes from the successful designated
job in this repository's candidate workflow at the exact candidate source.
"""
import hashlib
import io
import json
from pathlib import Path
import re
import subprocess
import zipfile

CHECKS = ('compatibility', 'attachment', 'invocation', 'observation_read',
          'finish_accepted', 'exit_zero', 'cleanup', 'export')
WORKFLOW = '.github/workflows/verify-release.yml'
ARTIFACT = 'interceptor-qualification'


def digest(raw):
    return 'sha256:' + hashlib.sha256(raw).hexdigest()


def validate_pin(pin, contract, integration):
    if not isinstance(pin, dict) or set(pin) != {'source_commit', 'archive_url', 'archive_sha256', 'interceptor_contract_version', 'harness'}:
        raise ValueError('qualification dependency fields are incomplete')
    harness = pin['harness']
    if (pin['interceptor_contract_version'] != integration or
            not re.fullmatch(r'[a-f0-9]{40}', pin['source_commit']) or
            not re.fullmatch(r'sha256:[a-f0-9]{64}', pin['archive_sha256']) or
            not re.fullmatch(r'https://github.com/intrusiveai/interceptor_sandbox/releases/download/v[0-9]+\.[0-9]+\.[0-9]+/interceptor_sandbox-v[0-9]+\.[0-9]+\.[0-9]+-linux_amd64.tar.gz', pin['archive_url']) or
            set(harness) != {'source_commit', 'image', 'contract'} or
            not re.fullmatch(r'[a-f0-9]{40}', harness['source_commit']) or
            not re.fullmatch(r'ghcr.io/intrusiveai/attack_harness@sha256:[a-f0-9]{64}', harness['image']) or
            harness['contract'] != contract):
        raise ValueError('qualification dependencies do not match the compiled contracts or immutable artifact formats')
    return pin


def read_pin(contract, integration):
    path = Path(__file__).resolve().parents[1] / 'release/interceptor-qualification.json'
    return validate_pin(json.loads(path.read_text()), contract, integration)


def validate_result(result, inventory, inventory_digest, run):
    from publish_host_release import asset_stem
    expected = {'schema_version': 1, 'passed': True, 'platform': 'linux/amd64',
                'source_commit': inventory['source_commit'], 'test_revision': inventory['source_commit'],
                'inventory_digest': inventory_digest, 'dependencies': inventory['qualification_dependencies'],
                'contract': inventory['contract'], 'interceptor_contract_version': inventory['interceptor_contract_version'],
                'operator_archive_sha256': inventory['assets'][asset_stem(inventory['version'], 'linux/amd64') + '.tar.gz'],
                'workflow_run_id': run['id'], 'workflow_run_attempt': run['run_attempt'],
                'checks': {name: True for name in CHECKS}}
    if any(result.get(key) != value for key, value in expected.items()):
        raise ValueError('qualification result differs from the candidate, dependencies, or successful workflow run')


def trusted_run(run, inventory, repository):
    return (run.get('head_sha') == inventory['source_commit'] and
            run.get('head_branch') == 'candidate-v' + inventory['version'] and
            run.get('event') in ('push', 'workflow_dispatch') and
            run.get('path') == WORKFLOW and run.get('status') == 'completed' and
            run.get('conclusion') == 'success' and
            run.get('head_repository', {}).get('full_name') == repository)


def unpack_result(raw, artifact):
    if len(raw) > 131072 or digest(raw) != artifact.get('digest'):
        raise ValueError('qualification artifact exceeds bounds or digest differs')
    with zipfile.ZipFile(io.BytesIO(raw)) as archive:
        entries = archive.infolist()
        if len(entries) != 1 or entries[0].filename != 'result.json' or entries[0].file_size > 65536:
            raise ValueError('qualification artifact has an unexpected inventory')
        return json.loads(archive.read(entries[0]))


def require_qualification(github, inventory, inventory_digest):
    validate_pin(inventory['qualification_dependencies'], inventory['contract'], inventory['interceptor_contract_version'])
    # Also bind the dependency file committed at the tested source. An edited
    # uploaded inventory cannot designate a different test dependency.
    import base64
    source = github.api('contents/release/interceptor-qualification.json?ref=' + inventory['source_commit'])
    if json.loads(base64.b64decode(source['content'], validate=False)) != inventory['qualification_dependencies']:
        raise ValueError('qualification dependencies differ from candidate source')
    runs = github.api('actions/workflows/verify-release.yml/runs?head_sha=' + inventory['source_commit'] + '&per_page=100')['workflow_runs']
    for run in runs:
        if not trusted_run(run, inventory, github.repository):
            continue
        jobs = github.api(f"actions/runs/{run['id']}/attempts/{run['run_attempt']}/jobs?per_page=100")['jobs']
        qualified = [job for job in jobs if job['name'] == 'qualify-interceptor']
        if len(qualified) != 1 or qualified[0]['conclusion'] != 'success':
            continue
        artifacts = github.api(f"actions/runs/{run['id']}/artifacts?per_page=100")['artifacts']
        artifacts = [a for a in artifacts if a['name'] == ARTIFACT and not a['expired']]
        if len(artifacts) != 1:
            continue
        artifact = artifacts[0]
        if artifact.get('workflow_run', {}).get('head_sha') != inventory['source_commit']:
            continue
        with __import__('tempfile').TemporaryDirectory(prefix='operator-qualification-') as directory:
            path = Path(directory) / 'artifact.zip'
            with path.open('wb') as output:
                subprocess.run(['gh', 'api', github.prefix + f"actions/artifacts/{artifact['id']}/zip"], stdout=output, check=True, timeout=60)
            # GitHub's authenticated artifact API supplies the archive, never a
            # caller-controlled URL or a JSON attachment from a PR.
            if path.stat().st_size > 131072:
                raise ValueError('qualification artifact exceeds bounds')
            result = unpack_result(path.read_bytes(), artifact)
        try:
            validate_result(result, inventory, inventory_digest, run)
        except ValueError:
            continue  # An older attempt can be stale; it is never a pass.
        return {'run_id': run['id'], 'run_attempt': run['run_attempt'], 'artifact_digest': artifact['digest']}
    raise ValueError('stable promotion requires successful matching Interceptor qualification; rerun the candidate qualification job')
