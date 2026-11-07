#!/usr/bin/env python3
"""Generate/check the self-contained interface; approve its exact bytes explicitly."""
import argparse
import json
import hashlib
import sys
from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'contracts/python'))
from operator_contracts.interface import build_document


def generate(root=ROOT):
    files = {p.name: p.read_bytes() for p in (root/'schemas').glob('*.schema.json')}
    for name in ('catalog.json', 'operations.json'):
        files[name] = (root/'schemas'/name).read_bytes()
    version = json.loads((root/'release/contract-lock.json').read_bytes())['package']['package_version']
    semantics = json.loads((root/'schemas/asyncapi-semantics.json').read_bytes())
    return (json.dumps(build_document(version, files, semantics), indent=2, ensure_ascii=False, sort_keys=True) + '\n').encode()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    parser.add_argument('--approve', action='store_true', help='write reviewed document identity into the lock')
    args = parser.parse_args()
    raw = generate()
    path = ROOT/'schemas/asyncapi.json'
    pin = {'package_version': json.loads(raw)['info']['version'], 'package_digest': 'sha256:' + hashlib.sha256(raw).hexdigest()}
    if args.check:
        if path.read_bytes() != raw or json.loads((ROOT/'release/contract-lock.json').read_bytes())['package'] != pin:
            raise SystemExit('AsyncAPI document or approved identity is stale; review changes before --approve')
    else:
        path.write_bytes(raw)
        if args.approve:
            (ROOT/'release/contract-lock.json').write_text(json.dumps({'api_version':'operator.dev/host-contract-lock/v2', 'package':pin}, indent=2)+'\n')
    print(json.dumps(pin))

if __name__ == '__main__':
    main()
