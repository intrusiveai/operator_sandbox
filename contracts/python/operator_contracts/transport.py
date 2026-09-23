"""Bounded FIFO codec, spool names and launch-wide sequence/ACK state; no I/O."""
import json
import re
from .validation import ContractError, ORDINARY_LIMIT, CONTROL_LIMIT, MAX_SAFE_INTEGER
from .startup import require

LANES = {'ordinary-in': (True, False, ORDINARY_LIMIT), 'ordinary-out': (False, False, ORDINARY_LIMIT),
         'control-in': (True, True, CONTROL_LIMIT), 'control-out': (False, True, CONTROL_LIMIT)}


def lane_info(lane):
    require(lane in LANES)
    return LANES[lane]


def validate_lane_message(protocol, lane, raw):
    host, control, _ = lane_info(lane)
    if control:
        return protocol.validate_control('host' if host else 'guest', raw)
    return protocol._validate_response_envelope(raw) if host else protocol.validate_request(raw)


def encode_frame(protocol, lane, raw):
    validate_lane_message(protocol, lane, raw)
    return len(raw).to_bytes(4, 'big') + raw


class FrameDecoder:
    def __init__(self, protocol, lane):
        self._maximum = lane_info(lane)[2]
        self._protocol, self._lane = protocol, lane
        self._header, self._body = bytearray(), bytearray()
        self._length = None
        self._closed = False

    def feed(self, chunk):
        """Return (consumed, one complete envelope or None); retain caller-owned tail."""
        if self._closed:
            raise ContractError('contract transport closed')
        consumed = 0
        try:
            require(type(chunk) is bytes)
            view = memoryview(chunk)
            if len(self._header) < 4:
                count = min(4 - len(self._header), len(view))
                self._header.extend(view[:count])
                consumed += count
                if len(self._header) < 4:
                    return consumed, None
                self._length = int.from_bytes(self._header, 'big')
                if not 0 < self._length <= self._maximum:
                    raise ContractError('contract encoding limit exceeded')
            count = min(len(view) - consumed, self._length - len(self._body))
            self._body.extend(view[consumed:consumed + count])
            consumed += count
            if len(self._body) < self._length:
                return consumed, None
            raw = bytes(self._body)
            validate_lane_message(self._protocol, self._lane, raw)
            self._header.clear()
            self._body.clear()
            self._length = None
            return consumed, raw
        except ContractError:
            self._closed = True
            self._body.clear()
            raise

    def end(self):
        """EOF after rendezvous is terminal, including on a frame boundary."""
        self._closed = True
        self._body.clear()
        raise ContractError('contract transport closed')


def spool_message_name(sequence, temporary=False):
    require(type(sequence) is int and 0 <= sequence <= MAX_SAFE_INTEGER and type(temporary) is bool)
    return f'.{sequence:020d}.tmp' if temporary else f'{sequence:020d}.json'


def parse_spool_message_name(name):
    match = re.fullmatch(r'([0-9]{20})\.json|\.([0-9]{20})\.tmp', name)
    require(match is not None)
    temporary = match.group(2) is not None
    sequence = int(match.group(2) if temporary else match.group(1))
    require(sequence <= MAX_SAFE_INTEGER)
    return sequence, temporary


def validate_spool_message(protocol, lane, name, raw):
    sequence, temporary = parse_spool_message_name(name)
    require(not temporary)
    message = validate_lane_message(protocol, lane, raw)
    require(message['seq'] == sequence)
    return message


class TransportState:
    """Single event-loop owner; queue admission, timers and physical I/O are external."""
    def __init__(self, protocol, role, campaign, launch):
        require(role in ('host', 'guest'))
        require(all(re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._:-]{0,127}', value) for value in (campaign, launch)))
        self._protocol, self._host, self._campaign, self._launch = protocol, role == 'host', campaign, launch
        self._received, self._published, self._acknowledged = [-1, -1], [-1, -1], [-1, -1]
        self._closed = False

    def close(self):
        self._closed = True

    def _record(self, lane, raw, publish):
        if self._closed:
            raise ContractError('contract transport closed')
        try:
            host, control, _ = lane_info(lane)
            require((host == self._host) == publish)
            message = validate_lane_message(self._protocol, lane, raw)
            index = int(control)
            positions = self._published if publish else self._received
            require(message['campaign_id'] == self._campaign and message['launch_id'] == self._launch)
            require(positions[index] < MAX_SAFE_INTEGER and message['seq'] == positions[index] + 1)
            positions[index] = message['seq']
        except ContractError:
            self.close()
            raise

    def accept(self, lane, raw):
        self._record(lane, raw, False)

    def record_published(self, lane, raw):
        self._record(lane, raw, True)

    @staticmethod
    def _positions(values):
        return {key: (None if value < 0 else value) for key, value in zip(('ordinary_seq', 'control_seq'), values)}

    def acknowledged(self):
        return self._positions(self._acknowledged)

    def ack_bytes(self):
        if self._closed:
            raise ContractError('contract transport closed')
        return json.dumps({'api_version': 'operator.dev/engine-spool-ack/v1alpha1', 'launch_id': self._launch,
                           **self._positions(self._received)}, separators=(',', ':')).encode()

    def apply_ack(self, raw):
        if self._closed:
            raise ContractError('contract transport closed')
        try:
            ack = self._protocol.validate_ack(raw)
            require(ack['launch_id'] == self._launch)
            next_positions = list(self._acknowledged)
            for index, key in enumerate(('ordinary_seq', 'control_seq')):
                value = ack[key]
                if value is None:
                    continue
                require(value <= self._published[index])
                next_positions[index] = max(value, next_positions[index])
            self._acknowledged = next_positions
        except ContractError:
            self.close()
            raise
