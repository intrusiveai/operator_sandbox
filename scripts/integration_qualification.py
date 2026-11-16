"""GitHub-backed admission of the candidate OpenAPI comparison.

The JSON report is evidence only when it comes from the successful designated
job in this repository's candidate workflow at the exact candidate source.
"""
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import zipfile

from interceptor_contract import validate_pin, CHECKS, ROOT, PIN, SOURCE, validate_source
WORKFLOW = '.github/workflows/verify-release.yml'
ARTIFACT = 'interceptor-contract-comparison'


def digest(raw):
    return 'sha256:' + hashlib.sha256(raw).hexdigest()


def read_pin(version):
    return validate_pin(json.loads((ROOT / PIN).read_text()), version)


def validate_result(result, inventory, run):
    expected = {'schema_version': 2, 'passed': True, 'test_worktree_modified': False,
                'source_commit': inventory['source_commit'],
                'interceptor_contract': inventory['interceptor_contract'],
                'contract_source': inventory['interceptor_contract_source'],
                'remote_verified': True,
                'workflow_run_id': run['id'], 'workflow_run_attempt': run['run_attempt'],
                'checks': CHECKS}
    if any(result.get(key) != value for key, value in expected.items()):
        raise ValueError('contract comparison differs from candidate source, contract or successful workflow run')


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


def require_qualification(github, inventory):
    validate_pin(inventory['interceptor_contract'], inventory['interceptor_contract_version'])
    validate_source(inventory['interceptor_contract_source'])
    # Bind approval and provenance to the candidate's source. The report verifies
    # that source's code against its exact contract, not an Interceptor build.
    import base64
    for path, expected in ((PIN, inventory['interceptor_contract']), (SOURCE, inventory['interceptor_contract_source'])):
        source = github.api('contents/' + path.as_posix() + '?ref=' + inventory['source_commit'])
        if json.loads(base64.b64decode(source['content'], validate=False)) != expected:
            raise ValueError('contract approval differs from candidate source')
    runs = github.api('actions/workflows/verify-release.yml/runs?head_sha=' + inventory['source_commit'] + '&per_page=100')['workflow_runs']
    for run in runs:
        if not trusted_run(run, inventory, github.repository):
            continue
        jobs = github.api(f"actions/runs/{run['id']}/attempts/{run['run_attempt']}/jobs?per_page=100")['jobs']
        qualified = [job for job in jobs if job['name'] == 'interceptor-contract']
        if len(qualified) != 1 or qualified[0]['conclusion'] != 'success':
            continue
        artifacts = github.api(f"actions/runs/{run['id']}/artifacts?per_page=100")['artifacts']
        artifacts = [a for a in artifacts if a['name'] == ARTIFACT and not a['expired']]
        if len(artifacts) != 1:
            continue
        artifact = artifacts[0]
        if artifact.get('workflow_run', {}).get('head_sha') != inventory['source_commit']:
            continue
        with tempfile.TemporaryDirectory(prefix='operator-qualification-') as directory:
            path = Path(directory) / 'artifact.zip'
            with path.open('wb') as output:
                subprocess.run(['gh', 'api', github.prefix + f"actions/artifacts/{artifact['id']}/zip"], stdout=output, check=True, timeout=60)
            # GitHub's authenticated artifact API supplies the archive, never a
            # caller-controlled URL or a JSON attachment from a PR.
            if path.stat().st_size > 131072:
                raise ValueError('qualification artifact exceeds bounds')
            result = unpack_result(path.read_bytes(), artifact)
        try:
            validate_result(result, inventory, run)
        except ValueError:
            continue  # An older attempt can be stale; it is never a pass.
        return {'run_id': run['id'], 'run_attempt': run['run_attempt'], 'artifact_digest': artifact['digest']}
    raise ValueError('stable promotion requires successful matching OpenAPI comparison; rerun the candidate contract job')
