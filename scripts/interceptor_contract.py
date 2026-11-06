#!/usr/bin/env python3
"""Adopt or verify Interceptor's exact OpenAPI bytes against Operator's integration.

Adoption is explicit and writes source files only after comparison succeeds.
Release verification re-fetches the selected immutable source; it never updates
the approval and never downloads an Interceptor executable or runs a campaign.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
from urllib.parse import quote

ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = 'intrusiveai/interceptor_sandbox'
REMOTE_PATH = 'internal/integrationcontract/contract.json'
PIN = Path('internal/interceptor/contract.json')
DOCUMENT = Path('internal/interceptor/openapi.json')
SOURCE = Path('release/interceptor-contract-source.json')
MAX_BYTES = 4 << 20
CHECKS = ['operation-inventory', 'go-wire-types', 'request-encoding',
          'response-decoding', 'capability-decoding', 'adapter-exchanges']


def identity(raw):
    if not raw or len(raw) > MAX_BYTES:
        raise ValueError('OpenAPI document exceeds its bound or is empty')
    doc = json.loads(raw)
    version = doc.get('info', {}).get('version')
    if doc.get('openapi') != '3.1.0' or not isinstance(version, str) or len(version) > 128 or not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', version):
        raise ValueError('expected OpenAPI 3.1.0 with an exact contract version')
    return {'interceptor_contract_version': version,
            'interceptor_contract_sha256': hashlib.sha256(raw).hexdigest()}


def validate_pin(pin, version=None):
    if (not isinstance(pin, dict) or set(pin) != {'interceptor_contract_version', 'interceptor_contract_sha256'} or
            not isinstance(pin['interceptor_contract_version'], str) or len(pin['interceptor_contract_version']) > 128 or
            not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', pin['interceptor_contract_version']) or
            not isinstance(pin['interceptor_contract_sha256'], str) or not re.fullmatch(r'[a-f0-9]{64}', pin['interceptor_contract_sha256']) or
            version is not None and pin['interceptor_contract_version'] != version):
        raise ValueError('invalid or mismatched Interceptor contract identity')
    return pin


def validate_source(source):
    if (not isinstance(source, dict) or set(source) != {'repository', 'ref', 'commit', 'path'} or
            source['repository'] != REPOSITORY or source['path'] != REMOTE_PATH or
            not isinstance(source['commit'], str) or not re.fullmatch(r'[a-f0-9]{40}', source['commit']) or
            not isinstance(source['ref'], str) or not source['ref'] or len(source['ref']) > 256):
        raise ValueError('invalid Interceptor contract source selection')
    return source


def api(path):
    raw = subprocess.check_output(['gh', 'api', 'repos/' + REPOSITORY + '/' + path], timeout=60)
    if len(raw) > MAX_BYTES * 2:
        raise ValueError('GitHub contract response exceeds its bound')
    return json.loads(raw)


def select(ref):
    if not ref or len(ref) > 256 or any(ord(c) < 32 for c in ref):
        raise ValueError('select an explicit Interceptor release tag or source revision')
    commit = api('commits/' + quote(ref, safe=''))['sha']
    return validate_source({'repository': REPOSITORY, 'ref': ref, 'commit': commit, 'path': REMOTE_PATH})


def fetch(source):
    validate_source(source)
    result = api('contents/' + source['path'] + '?ref=' + source['commit'])
    if result.get('encoding') != 'base64' or result.get('size', MAX_BYTES + 1) > MAX_BYTES:
        raise ValueError('GitHub did not return a bounded contract file')
    raw = base64.b64decode(''.join(result['content'].split()), validate=True)
    if len(raw) != result['size']:
        raise ValueError('downloaded contract length differs')
    identity(raw)
    return raw


def compare(raw, root=ROOT):
    identity(raw)
    with tempfile.TemporaryDirectory(prefix='operator-openapi-') as directory:
        document = Path(directory) / 'contract.json'
        document.write_bytes(raw)
        environment = dict(os.environ, OPERATOR_INTERCEPTOR_CONTRACT=str(document))
        subprocess.run(['go', 'test', '-count=1', '-timeout', '2m', '-run', '^TestInterceptorContract',
                        './internal/interceptor', './internal/capabilities', './internal/attemptadapter'],
                       cwd=root, env=environment, check=True, timeout=180)


def write(path, raw):
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as output:
        temporary = Path(output.name)
        output.write(raw)
    try:
        temporary.chmod(0o644)
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def encoded(value):
    return (json.dumps(value, indent=2) + '\n').encode()


def adopt(source, raw, root=ROOT):
    validate_source(source)
    pin = identity(raw)
    compare(raw, root)
    # Pin last: a partial update cannot pass verify or a release build. Git is
    # the transaction/review boundary for the three source files.
    write(root / DOCUMENT, raw)
    write(root / SOURCE, encoded(source))
    write(root / PIN, encoded(pin))
    return pin


def verify(root=ROOT, remote=False):
    raw = (root / DOCUMENT).read_bytes()
    pin = validate_pin(json.loads((root / PIN).read_text()))
    source = validate_source(json.loads((root / SOURCE).read_text()))
    if identity(raw) != pin:
        raise ValueError('committed OpenAPI bytes differ from the approved contract identity; explicitly adopt the contract')
    if remote and fetch(source) != raw:
        raise ValueError('selected remote contract bytes differ from the approved copy')
    compare(raw, root)
    return pin, source


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    a = sub.add_parser('adopt'); a.add_argument('--ref', required=True)
    v = sub.add_parser('verify'); v.add_argument('--fetch', action='store_true'); v.add_argument('--output', type=Path)
    args = parser.parse_args()
    if args.command == 'adopt':
        source = select(args.ref)
        pin = adopt(source, fetch(source))
        print(json.dumps({'status': 'adopted', **pin, 'source': source}))
        return
    pin, source = verify(remote=args.fetch)
    result = {'schema_version': 2, 'passed': True, 'interceptor_contract': pin, 'contract_source': source,
              'source_commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
              'test_worktree_modified': bool(subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=normal'], cwd=ROOT)),
              'remote_verified': args.fetch, 'checks': CHECKS,
              'workflow_run_id': int(os.environ.get('GITHUB_RUN_ID', '0')),
              'workflow_run_attempt': int(os.environ.get('GITHUB_RUN_ATTEMPT', '0'))}
    if args.output:
        write(args.output, encoded(result))
    print(json.dumps(result))


if __name__ == '__main__':
    main()
