"""Strict JSON and catalog validation with no runtime schema fetching."""
import json
import math
from decimal import Decimal, InvalidOperation
from pathlib import Path

from jsonschema import Draft202012Validator, ValidationError, SchemaError
from referencing import Registry, Resource
from referencing.exceptions import Unresolvable

ORDINARY_LIMIT = 4 << 20
CONTROL_LIMIT = 64 << 10
MAX_DEPTH = 32
MAX_SAFE_INTEGER = (1 << 53) - 1

SAFE_PATH_FIELDS = ' name arguments input injection_input value encoding text data media_type artifact_receipt operation_id scenario_id parent_attempt_receipt_id caller_principal_id pre_actions action_id action_type parameters surface mode scope selector placement operation pointer retain_injections observation_selection rationale strategy_provenance_ref technique_refs finish_reason assessment unavailable reason status summary objectives objective_id outcome hypotheses hypothesis_id objective_refs provenance origin parent_hypothesis_id claims claim_id interpretation assurance confidence attempt_receipt_refs attempt_receipt_id observation_refs entry_id range offset length uncertainty_refs coverage alternative uncertainties gap_id description input_refs record_refs record_kind record payload carrier digest generation thread_id request_id attempt_id attempt_index binding api_version kind artifact record_receipt tool_name call_ordinal method path predicates equals contains max_bytes receipt_id reference_id source_session checkpoint_id label continuation limit '

class ContractError(ValueError):
    """Bounded rule details; never copies validator messages or submitted values."""
    def __init__(self, message, *, reason="invalid_arguments", field_path="", correction=""):
        super().__init__(message)
        self.reason = reason
        self.field_path = safe_argument_path(field_path)
        self.correction = correction


def safe_argument_path(path):
    if len(path)>256 or path and not path.startswith('/'): return ''
    allowed = set(SAFE_PATH_FIELDS.split())
    return path if all(part in allowed or part.isascii() and part.isdigit() and len(part)<=4
                       for part in path.split('/')[1:]) else ''



def decode(raw: bytes, maximum: int = ORDINARY_LIMIT):
    if type(maximum) is not int or maximum <= 0 or len(raw) > maximum:
        raise ContractError("contract encoding limit exceeded")
    try:
        text = raw.decode("utf-8", errors="strict")
        depth, quoted, escaped = 0, False, False
        for char in text:
            if quoted:
                if escaped: escaped = False
                elif char == "\\": escaped = True
                elif char == '"': quoted = False
            elif char == '"': quoted = True
            elif char in "[{":
                depth += 1
                if depth > MAX_DEPTH: raise ContractError("contract encoding limit exceeded")
            elif char in "]}": depth -= 1

        class Pairs(list): pass
        def pairs(items): return Pairs(items)
        def objects(value, path=""):
            if isinstance(value, Pairs):
                result = {}
                for key, child in value:
                    if key in result:
                        raise ContractError("duplicate_json_key: Supply each JSON key once; duplicate keys are rejected before decoding can discard them.", reason="duplicate_json_key", field_path=path+"/"+key, correction="Supply each JSON key once; duplicate keys are rejected before decoding can discard them.")
                    result[key] = objects(child,path+"/"+key)
                return result
            if isinstance(value,list): return [objects(child,path+"/"+str(i)) for i,child in enumerate(value)]
            return value

        def number(text):
            value = float(text)
            if not math.isfinite(value) or value.is_integer() and abs(value) > MAX_SAFE_INTEGER:
                raise ContractError("invalid contract JSON")
            if value == 0:
                mantissa = text.lower().split("e", 1)[0]
                if any(char in "123456789" for char in mantissa):
                    raise ContractError("invalid contract JSON")
                return 0
            exact = Decimal(text)
            # Cross-document $refs may select the standard draft validator.
            # Represent exact integers as int, never a rounded binary64 float.
            return int(exact) if exact == exact.to_integral_value() else exact

        def bad_constant(_):
            raise ContractError("invalid contract JSON")

        value = json.loads(text, object_pairs_hook=pairs, parse_int=number, parse_float=number, parse_constant=bad_constant)

        value = objects(value)

        def unicode_check(item):
            if isinstance(item, str): item.encode("utf-8", errors="strict")
            elif isinstance(item, list):
                for child in item: unicode_check(child)
            elif isinstance(item, dict):
                for key, child in item.items(): unicode_check(key); unicode_check(child)
        unicode_check(value)
        return value
    except (ValueError, UnicodeError, OverflowError, RecursionError, InvalidOperation) as error:
        if isinstance(error, ContractError): raise
        raise ContractError("invalid contract JSON") from None


def _offline(_):
    raise Unresolvable("schema absent from installed catalog")


class Catalog:
    def __init__(self, directory: Path):
        directory = Path(directory)
        self._load(lambda name: (directory / name).read_bytes())

    @classmethod
    def _from_resources(cls, resources):
        result = cls.__new__(cls)
        result._load(lambda name: resources[name])
        return result

    def _load(self, read):
        try:
            entries = decode(read("catalog.json"))
            if not isinstance(entries, dict) or not entries: raise ContractError("invalid installed contract catalog")
            from .canonical import _object_digest
            self._digest = _object_digest(entries, ORDINARY_LIMIT)
            documents, seen = {}, set()
            registry = Registry(retrieve=_offline)
            for uri, name in entries.items():
                if not isinstance(name, str) or "/" in name or "\\" in name or name in (".", "..") or not name.endswith(".schema.json") or name in seen:
                    raise ContractError("invalid installed contract catalog")
                seen.add(name)
                raw = read(name)
                schema = decode(raw)
                if schema.get("$id") != uri or schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
                    raise ContractError("invalid installed contract catalog")
                # Schemas contain ordinary safe integer bounds; convert their exact
                # numbers back to standard values for metaschema/resource handling.
                schema = json.loads(raw)
                Draft202012Validator.check_schema(schema)
                documents[uri] = schema
                registry = registry.with_resource(uri, Resource.from_contents(schema))
            # Resolve every schema reference eagerly, including unused branches.
            for uri, schema in documents.items():
                resolver = registry.resolver(uri)
                def references(node):
                    if isinstance(node, dict):
                        if "$ref" in node: resolver.lookup(node["$ref"])
                        for child in node.values(): references(child)
                    elif isinstance(node, list):
                        for child in node: references(child)
                references(schema)
            self._validators = {uri: Draft202012Validator(schema, registry=registry) for uri, schema in documents.items()}
        except (OSError, KeyError, ValueError, TypeError, AttributeError, Unresolvable, SchemaError) as error:
            raise ContractError("invalid installed contract catalog") from None

    def ids(self):
        return sorted(self._validators)

    def validate(self, schema_id: str, raw: bytes, maximum: int = ORDINARY_LIMIT):
        if schema_id not in self._validators: raise ContractError("invalid installed contract catalog")
        value = decode(raw, maximum)
        self.validate_value(schema_id, value)
        return value

    def validate_value(self, schema_id: str, value):
        """Validate an already strictly decoded value; not a raw-wire entry point."""
        if schema_id not in self._validators: raise ContractError("invalid installed contract catalog")
        try:
            self._validators[schema_id].validate(value)
        except ValidationError as error:
            while error.context: error=error.context[0]
            parts=[str(x) for x in error.absolute_path]
            if error.validator == 'required' and isinstance(error.instance,dict):
                missing=sorted(set(error.validator_value)-set(error.instance))
                if missing: parts.append(missing[0])
            raise ContractError("invalid_structure: Supply the required fields with the documented types and no unknown fields.",reason="invalid_structure",field_path='/'+ '/'.join(parts) if parts else '',correction="Supply the required fields with the documented types and no unknown fields.") from None
        except Unresolvable:
            raise ContractError("invalid installed contract catalog") from None
        return value
