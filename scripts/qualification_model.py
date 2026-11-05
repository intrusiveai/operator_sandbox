"""Three controlled model turns: invoke, read the actual receipt, then finish.

Uses the same tool sequence as TestPythonAttemptOriginWorkflow. No provider or
secret-store access. Unexpected calls fail the qualification rather than retry.
"""
import base64
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import ssl
import threading


class ModelFixture:
    def __init__(self, cert, key):
        self.turns = 0
        self.receipt = None
        self.observed = False
        self.failures = []
        self.seam_calls = 0
        fixture = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                if self.path != '/guardrail':
                    self.send_error(404)
                    return
                fixture.seam_calls += 1
                raw = b'{"answer":"QA_CONFIG_OK"}'
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def do_POST(self):
                try:
                    length = int(self.headers.get('Content-Length', '0'))
                    if not 0 < length <= 8 * 1024 * 1024:
                        raise ValueError('fixture request size')
                    request = json.loads(self.rfile.read(length))
                    result = fixture.reply(request)
                    raw = json.dumps(result).encode()
                    self.send_response(200)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                except Exception as error:
                    fixture.failures.append(str(error))
                    self.send_error(500, 'controlled fixture failed')

        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key)
        self.server.socket = context.wrap_socket(self.server.socket, server_side=True)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.url = f'https://127.0.0.1:{self.server.server_port}/v1/chat/completions'

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *args):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)

    def reply(self, request):
        def tool_result(call):
            matches = [m for m in request['messages'] if m.get('role') == 'tool' and m.get('tool_call_id') == call]
            if len(matches) != 1:
                raise ValueError('missing or duplicate tool result: ' + call)
            return json.loads(matches[0]['content'])

        self.turns += 1
        if self.turns == 1:
            name, call = 'attempt_execute', 'invoke'
            args = {'origin': 'scenario', 'scenario_id': 'scenario-marker', 'operation_id': 'invoke',
                    'input': {'encoding': 'json', 'value': {'query': 'Reply QA_CONFIG_OK'}}}
        elif self.turns == 2:
            execution = tool_result('invoke')
            self.receipt = execution['receipt_id']
            entries = [e for e in execution['feedback']['entries'] if e['kind'] == 'target_output']
            if len(entries) != 1:
                raise ValueError('missing target output')
            name, call = 'observation_read', 'observe'
            args = {'receipt_id': self.receipt, 'entry_id': entries[0]['entry_id'], 'offset': 0, 'max_bytes': 8192}
        elif self.turns == 3:
            observed = tool_result('observe')
            content = observed['content'].encode('utf-8') if observed.get('encoding') == 'utf8' else base64.b64decode(observed['content'], validate=True)
            if observed['availability'] != 'available' or b'QA_CONFIG_OK' not in content:
                raise ValueError('controlled target observation was not read')
            self.observed = True
            name, call = 'finish', 'finish'
            args = {'finish_reason': 'objectives-addressed', 'assessment': {'summary': 'Observed QA_CONFIG_OK in the permitted target output.',
                    'objectives': [{'objective_id': 'objective-marker', 'outcome': 'supported', 'summary': 'The controlled target returned its marker.', 'attempt_receipt_refs': [self.receipt]}]}}
        else:
            raise ValueError('unexpected additional model call')
        return {'id': 'chatcmpl-qualification', 'object': 'chat.completion', 'created': 100, 'model': 'test-model',
                'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': None,
                    'tool_calls': [{'id': call, 'type': 'function', 'function': {'name': name, 'arguments': json.dumps(args)}}]},
                    'finish_reason': 'tool_calls'}], 'usage': {'prompt_tokens': 100, 'completion_tokens': 20, 'total_tokens': 120}}
