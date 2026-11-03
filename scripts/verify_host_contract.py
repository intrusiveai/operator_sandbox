#!/usr/bin/env python3
"""Verify contract identities from a native, archived Operator executable offline."""
import argparse
import hashlib
import json
import platform
from pathlib import Path
import subprocess
import tarfile
import tempfile
from publish_host_release import integration_version, contract_version


def digest(path):
    return 'sha256:' + hashlib.sha256(path.read_bytes()).hexdigest()


def verify(candidate):
    candidate = candidate.resolve()
    host = platform.system().lower() + '/' + {'x86_64':'amd64', 'aarch64':'arm64', 'arm64':'arm64'}.get(platform.machine(), '')
    metadata = json.loads((candidate/'release.json').read_text())
    if metadata['platform'] != host:
        raise ValueError('packaged executable requires a matching native runner')
    integration_version(metadata.get('interceptor_contract_version'))
    contract_version(metadata.get('contract'))
    with tempfile.TemporaryDirectory(prefix='operator-contract-') as temp:
        root = Path(temp)
        archive = root/'operator.tar.gz'
        subprocess.run([str(candidate/'bin/operatorctl'), 'release', 'archive', '--directory', str(candidate), '--output', str(archive)], check=True, capture_output=True, timeout=120)
        with tarfile.open(archive) as source:
            source.extractall(root/'extracted', filter='data')
        installed = root/'extracted/operator_sandbox'
        subprocess.run([str(installed/'bin/operatorctl'), 'release', 'check', '--directory', str(installed)], check=True, capture_output=True, timeout=120)
        actual = json.loads(subprocess.check_output([str(installed/'bin/operatorctl'), 'version'], timeout=30))
        expected = {'version':metadata['version'], 'source_commit':metadata['source_commit'],
                    'contract_version':metadata['contract']['package_version'], 'contract_digest':metadata['contract']['package_digest'],
                    'interceptor_contract_version':metadata['interceptor_contract_version']}
        if any(actual.get(k) != v for k,v in expected.items()):
            raise ValueError('archived executable contract/source identity differs from metadata')
        return dict(expected, platform=host, archive_sha256=digest(archive), binary_sha256=digest(installed/'bin/operatorctl'), checks=['archive-integrity','native-version'])


def validate_report(report, host, archive_sha256):
    expected={'platform':host['platform'], 'version':host['version'], 'source_commit':host['source_commit'],
              'interceptor_contract_version':host['interceptor_contract_version'], 'contract_version':host['contract']['package_version'],
              'contract_digest':host['contract']['package_digest'], 'archive_sha256':archive_sha256, 'checks':['archive-integrity','native-version']}
    binary=next(f['digest'] for f in host['files'] if f['path']=='bin/operatorctl')
    if any(report.get(k)!=v for k,v in expected.items()) or report.get('binary_sha256')!=binary:
        raise ValueError('native contract verification does not identify this packaged artifact')


if __name__ == '__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--candidate',type=Path,required=True);p.add_argument('--output',type=Path,required=True)
    a=p.parse_args();result=verify(a.candidate);a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(result,indent=2)+'\n')
