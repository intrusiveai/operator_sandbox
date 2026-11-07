#!/usr/bin/env python3
"""Offline interface and implementation conformance, without a counterpart build pin."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import jsonschema
ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT/'contracts/python'))
from operator_contracts import Protocol
from operator_contracts.interface import document_identity
from generate_asyncapi import generate


def check_document(root=ROOT):
    raw = (root/'schemas/asyncapi.json').read_bytes()
    expected = json.loads((root/'release/contract-lock.json').read_bytes())['package']
    if document_identity(raw) != expected or raw != generate(root):
        raise ValueError('interface differs from approved bytes or implementation resources')
    schema = json.loads((root/'scripts/vendor/asyncapi/3.0.0.json').read_bytes())
    jsonschema.Draft7Validator(schema).validate(json.loads(raw))
    # Compile the declared 2020-12 payloads; the AsyncAPI metaschema does not
    # establish their dialect semantics or the products' stateful behavior.
    protocol = Protocol.from_interface(raw, expected)
    if protocol.registry_digests() != Protocol(root/'schemas').registry_digests():
        raise ValueError('implementation registry differs from interface')
    return expected


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--document-only', action='store_true', help='syntax/resource comparison only; not functional conformance')
    p.add_argument('--output', type=Path)
    a=p.parse_args()
    identity=check_document()
    checks=['approved-exact-bytes','asyncapi-3.0.0','json-schema-2020-12','implementation-resource-comparison']
    if not a.document_only:
        env=dict(os.environ, OPERATOR_CONTROLLED_PEER_TEST='1', OPERATOR_PYTHON_PEER_TEST='1', OPERATOR_TEST_PYTHON=sys.executable)
        commands=[['go','test','-count=1','-timeout','10m','./contracts','./internal/campaignservice','./internal/attemptadapter','./internal/startup'],
                  [sys.executable,'-m','unittest','discover','-s','contracts/python/tests'],
                  ['go','test','-count=1','-timeout','10m','-run','^Test(Simple|ExplicitOrigin|ConfiguredInput|ConclusionObservation|RuntimeDiagnostic|AssessmentRetention|FinishAcknowledgement|Service|PythonSpoolInteroperability|PythonFIFOInteroperability)', './internal/preparation','./internal/transport']]
        env['PYTHONPATH']=str(ROOT/'contracts/python')
        for command in commands:
            subprocess.run(command,cwd=ROOT,env=env,check=True,timeout=660)
        checks += ['go-python-shared-fixtures','production-operation-dispatch','controlled-startup-admission','controlled-direct-evidence-finish-export','fifo-spool-peers','stateful-admission-replay-visibility-finalization']
    source = subprocess.check_output(['git','rev-parse','HEAD'], cwd=ROOT, text=True).strip()
    dirty = bool(subprocess.check_output(['git','status','--porcelain'], cwd=ROOT, text=True).strip())
    result={'contract':identity,'checks':checks,'functional_conformance':not a.document_only,
            'tested_source': {'commit':source, 'dirty':dirty},
            'scope':'controlled local tests; not live provider or production Harness qualification'}
    if a.output:
        a.output.parent.mkdir(parents=True,exist_ok=True)
        a.output.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result))

if __name__=='__main__':main()
