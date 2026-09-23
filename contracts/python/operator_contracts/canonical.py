"""RFC 8785 serialization within the contract's stricter JSON input domain."""
import hashlib
import json
from decimal import Decimal
from .validation import decode, ContractError, ORDINARY_LIMIT


def raw_digest(raw):
    """Hash exact bytes, without parsing or normalization."""
    return 'sha256:' + hashlib.sha256(raw).hexdigest()


def _number(value):
    value = float(value)
    if value == 0:
        return '0'
    sign = '-' if value < 0 else ''
    # repr supplies the shortest round-trip binary64 digits. Apply ECMAScript's
    # decimal/exponent layout, independently of Python's display thresholds.
    mantissa, _, exponent = repr(abs(value)).partition('e')
    integer, _, fraction = mantissa.partition('.')
    digits = integer + fraction
    point = len(integer) + (int(exponent) if exponent else 0)
    leading = len(digits) - len(digits.lstrip('0'))
    digits = digits.lstrip('0').rstrip('0')
    point -= leading
    if 0 < point <= 21:
        body = digits + '0' * (point - len(digits)) if point >= len(digits) else digits[:point] + '.' + digits[point:]
    elif -6 < point <= 0:
        body = '0.' + '0' * -point + digits
    else:
        body = digits[0] + ('.' + digits[1:] if len(digits) > 1 else '')
        body += 'e' + ('+' if point - 1 >= 0 else '') + str(point - 1)
    return sign + body


def _canonical_value(value, maximum):
    """Private: strictly decoded values and schema-defined projections only."""
    chunks, size = [], 0

    def write(raw):
        nonlocal size
        size += len(raw)
        if size > maximum:
            raise ContractError('contract encoding limit exceeded')
        chunks.append(raw)

    def string(value):
        return json.dumps(value, ensure_ascii=False, separators=(',', ':')).encode('utf-8')

    def emit(value):
        if value is None:
            write(b'null')
        elif type(value) is bool:
            write(b'true' if value else b'false')
        elif type(value) is str:
            write(string(value))
        elif type(value) in (int, Decimal):
            write(_number(value).encode('ascii'))
        elif type(value) is list:
            write(b'[')
            for index, child in enumerate(value):
                if index: write(b',')
                emit(child)
            write(b']')
        elif type(value) is dict:
            write(b'{')
            for index, key in enumerate(sorted(value, key=lambda key: key.encode('utf-16-be'))):
                if index: write(b',')
                write(string(key))
                write(b':')
                emit(value[key])
            write(b'}')
        else:
            raise ContractError('invalid contract JSON')
    emit(value)
    return b''.join(chunks)


def canonicalize(raw, maximum=ORDINARY_LIMIT):
    """Validate JSON then emit canonical bytes; omit no fields. Bound input/output.

    Validate the applicable schema first. Binary64 rounding must never repair a
    schema-invalid number before admission.
    """
    return _canonical_value(decode(raw, maximum), maximum)


def canonical_digest(raw, maximum=ORDINARY_LIMIT):
    return raw_digest(canonicalize(raw, maximum))


def _object_digest(value, maximum):
    return raw_digest(_canonical_value(value, maximum))
