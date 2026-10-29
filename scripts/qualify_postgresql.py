#!/usr/bin/env python3
"""Controlled Operator/Python Harness workflow against native Interceptor PostgreSQL.

Requires a local native Interceptor release archive and Docker. Creates isolated
images/state, uses fixture model responses, and retains exported test evidence.
No live cloud/model calls or release publication.
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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--interceptor-release', type=Path, required=True)
    parser.add_argument('--interceptor-source', type=Path, default=Path(__file__).resolve().parents[2]/'interceptor_sandbox')
    parser.add_argument('--base-image', default='python:3.12-slim')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    operator = Path(__file__).resolve().parents[1]
    native = args.interceptor_source.resolve()
    sys.path.insert(0, str(native/'scripts'))
    from embedding_fixture import EmbeddingFixture
    from publish_release import read_archive
    metadata = read_archive(args.interceptor_release)
    host_arch = {'aarch64':'arm64','arm64':'arm64','x86_64':'amd64'}.get(platform.machine())
    if metadata['platform'] != platform.system().lower()+'/'+str(host_arch):
        raise ValueError('Interceptor archive must match the native host')
    with closing(socket.socket()) as check:
        check.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
        check.bind(('127.0.0.1',8080))  # Refuse to interfere with an existing Interceptor.
    output = args.output.resolve()
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    suffix = uuid.uuid4().hex
    target, wrapper = 'operator-pg-target:'+suffix, 'operator-pg-wrapper:'+suffix
    process = None
    with tempfile.TemporaryDirectory(prefix='operator-pg-') as temporary, EmbeddingFixture() as model:
        work = Path(temporary).resolve()
        env = dict(os.environ, INTERCEPTOR_DATA_DIR=str(work/'data'))
        def run(*command, timeout=900):
            result = subprocess.run(command, cwd=operator, env=env, text=True, capture_output=True, timeout=timeout)
            if result.returncode:
                raise RuntimeError(f'{command[0:3]} failed:\n{result.stdout}\n{result.stderr}')
            return result.stdout.strip()
        try:
            install=work/'install';install.mkdir()
            with tarfile.open(args.interceptor_release) as archive:
                archive.extractall(install,filter='data')  # read_archive validated exact inventory.
            cli=str(install/'interceptor_sandbox/interceptor')
            base=json.loads(run('docker','image','inspect',args.base_image))[0]
            if base['Os']!='linux' or base['Architecture']!=host_arch:
                raise ValueError('base image must already be local and native Linux')
            example=native/'examples/postgresql/python'
            context=work/'target';shutil.copytree(example/'target',context)
            dockerfile=context/'Dockerfile'
            dockerfile.write_text('FROM '+args.base_image+'\n'+dockerfile.read_text().split('\n',1)[1])
            run('docker','build','--pull=false','--tag',target,str(context))
            image=run('docker','image','inspect','--format','{{.Id}}',target)
            blueprint=work/'environment';shutil.copytree(example/'environment',blueprint)
            path=blueprint/'environment.yaml';text=path.read_text()
            placeholder='interceptor-adapter-python:local@sha256:'+'0'*64
            if placeholder not in text or text.count('    - id: ask\n')!=1:
                raise ValueError('native example changed; review delivery annotation')
            text=text.replace(placeholder,target+'@'+image).replace('api_version: interceptor.dev/v1alpha1','api_version: interceptor.dev/v1alpha2',1)
            text=text.replace('    - id: ask\n','    - id: ask\n      delivery_asset: assets/ask.json\n')
            path.write_text(text)
            (blueprint/'assets/ask.json').write_text(json.dumps({
                'description':'Invoke the controlled PostgreSQL support application',
                'input':{'media_type':'application/json','encoding':'json','max_bytes':65536,'basis':'synthetic',
                         'schema':{'type':'object','additionalProperties':False,'required':['message'],'properties':{'message':{'type':'string'}}}},
                'output':{'media_type':'application/json','encoding':'json','max_bytes':65536,'basis':'unknown'}}))
            run(cli,'environment','validate',str(blueprint));run(cli,'environment','seal',str(blueprint))
            run(cli,'app','build','--environment',str(blueprint),'--tag',wrapper,'--json')
            run(cli,'app','verify','--environment',str(blueprint))
            env['OPERATOR_PG_MODEL_KEY']=model.keys['generation']
            with (output/'interceptor.log').open('w') as log:
                process=subprocess.Popen([cli,'run','--environment',str(blueprint),'--model-provider','openai-chat',
                    '--provider-auth','api-key-env','--model-api-key-env','OPERATOR_PG_MODEL_KEY',
                    '--provider-base-url',model.urls['generation'],'--allow-model','chat-fixture','--feedback-profile','oracle-assisted'],
                    cwd=work,env=env,stdout=log,stderr=subprocess.STDOUT)
                deadline=time.monotonic()+90
                while 'Interceptor local API:' not in (output/'interceptor.log').read_text():
                    if process.poll() is not None:raise RuntimeError('Interceptor exited before readiness; see interceptor.log')
                    if time.monotonic()>deadline:raise RuntimeError('Interceptor readiness deadline exceeded')
                    time.sleep(.2)
                testenv=dict(os.environ,OPERATOR_HARNESS_INTEGRATION='1',OPERATOR_POSTGRESQL_INTEGRATION='1',
                    OPERATOR_TEST_PYTHON=str(operator/'.venv/bin/python'),OPERATOR_POSTGRESQL_OUTPUT=str(output))
                with (output/'operator-test.log').open('w') as testlog:
                    tested=subprocess.run(['go','test','./internal/preparation','-run','^TestPythonPostgreSQLWorkflow$','-count=1','-v','-timeout','5m'],
                        cwd=operator,env=testenv,stdout=testlog,stderr=subprocess.STDOUT,timeout=330)
                if tested.returncode:raise RuntimeError('Operator workflow failed; see operator-test.log')
                if model.failures:raise RuntimeError('fixture provider routing failed')
                (output/'sources.json').write_text(json.dumps({'interceptor':metadata,'operator_source':run('git','rev-parse','HEAD'),
                    'operator_worktree_modified':bool(run('git','status','--porcelain')),'harness_source':run('git','-C',str(operator.parent/'attack_harness'),'rev-parse','HEAD'),
                    'verification':'real Python Harness process and real Interceptor PostgreSQL container; controlled model responses; no Harness Docker qualification'},indent=2)+'\n')
        finally:
            if process and process.poll() is None:
                process.send_signal(signal.SIGINT)
                try:process.wait(timeout=45)
                except subprocess.TimeoutExpired:
                    process.kill();process.wait()
            # Clean only containers belonging to this unique test wrapper,
            # including a leftover after an abnormal host-process exit.
            ids=run('docker','ps','-aq','--filter','ancestor='+wrapper).splitlines()
            if ids:run('docker','rm','-f',*ids)
            for image in (wrapper,target):
                subprocess.run(['docker','image','rm',image],capture_output=True,timeout=60)
    print(json.dumps({'passed':True,'evidence':str(output)}))

if __name__=='__main__':main()
