"""Ordinary wire validation and stateless request/response consistency checks."""
import base64
import binascii
from copy import deepcopy
from datetime import datetime
from pathlib import Path

from .validation import Catalog, ContractError, ORDINARY_LIMIT
from .assessment import check_record, validate_conclusion, validate_completion
from .schema_ids import (
    ENGINE_PIPE_REQUEST_SCHEMA, ENGINE_PIPE_RESPONSE_SCHEMA, OPERATION_REGISTRY_SCHEMA,
)


def _require(condition):
    if not condition:
        raise ContractError("contract message consistency check failed")


def _part(encoded):
    try:
        value = base64.b64decode(encoded, validate=True)
    except (ValueError, binascii.Error):
        raise ContractError("contract message consistency check failed") from None
    _require(len(value) <= 262144 and base64.b64encode(value).decode("ascii") == encoded)
    return value


def _description(body):
    _require(len(body.get("description", "").encode("utf-8")) <= 4096)


def _conclusion_artifact(body):
    if body["purpose"] == "conclusion":
        artifact = body["artifact"]
        _require(artifact["size_bytes"] <= 1 << 20 and artifact["media_type"] == "application/json")


def _metadata(snapshot, campaign):
    _require(snapshot["campaign_id"] == campaign)
    _description(snapshot)
    # The schema requires the UTC Z spelling; this checks the calendar date.
    try:
        datetime.fromisoformat(snapshot["created_at"])
    except ValueError:
        raise ContractError("contract message consistency check failed") from None


class Protocol:
    """Not an admission controller: sequencing, durable effects and policy remain host work."""

    def __init__(self, directory: Path):
        self._catalog = Catalog(directory)
        try:
            registry = self._catalog.validate(
                OPERATION_REGISTRY_SCHEMA, (Path(directory) / "operations.json").read_bytes()
            )
            names = [item["name"] for item in registry["operations"]]
            if names != sorted(set(names)):
                raise ContractError("invalid installed contract catalog")
            for operation in registry["operations"]:
                for field in ("request_schema", "result_schema", "error_schema"):
                    if operation[field] not in self._catalog.ids():
                        raise ContractError("invalid installed contract catalog")
            self._operations = {item["name"]: item for item in registry["operations"]}
        except (OSError, ContractError):
            raise ContractError("invalid installed contract catalog") from None

    def operations(self):
        """Return a copy so consumers cannot mutate installed dispatch metadata."""
        return deepcopy(list(self._operations.values()))

    def validate_conclusion(self, raw: bytes):
        return validate_conclusion(self, raw)

    def validate_completion(self, **retained_records):
        return validate_completion(self, **retained_records)

    def validate_ack(self, raw: bytes):
        """Syntax/size only; launch and monotonic positions require transport state."""
        return self._catalog.validate("urn:operator:schema:engine-spool-ack:v1alpha1", raw, 1024)

    def _check_registered(self, operation, schema_field, value):
        # Preserve exact Decimal values; serializing via a generic encoder is unsafe.
        self._catalog.validate_value(operation[schema_field], value)

    def validate_request(self, raw: bytes):
        message = self._catalog.validate(ENGINE_PIPE_REQUEST_SCHEMA, raw)
        operation = self._operations.get(message["operation"])
        if operation is None:
            raise ContractError("invalid installed contract catalog")
        if len(raw) > operation["max_request_bytes"]:
            raise ContractError("contract encoding limit exceeded")
        body = message["body"]
        self._check_registered(operation, "request_schema", body)
        name = operation["name"]
        if name == "engine.record_append":
            check_record(body)
        elif name == "engine.attempt_execute":
            _require(body["request_id"] == message["operation_id"])
        elif name == "engine.artifact_begin":
            _conclusion_artifact(body)
        elif name == "engine.artifact_put_part":
            data = _part(body["content"])
            _require(len(data) > 0 and body["offset"] + len(data) <= 16 << 20)
        elif name == "engine.snapshot_request":
            _description(body)
        return message

    def validate_response(self, request: bytes, response: bytes):
        req = self.validate_request(request)
        message = self._catalog.validate(ENGINE_PIPE_RESPONSE_SCHEMA, response)
        for field in ("campaign_id", "launch_id", "run_revision", "call_id", "operation_id", "operation"):
            _require(message[field] == req[field])
        operation = self._operations[req["operation"]]
        if len(response) > operation["max_result_bytes"]:
            raise ContractError("contract encoding limit exceeded")
        if "error" in message:
            self._check_registered(operation, "error_schema", message["error"])
            _require(message["error"]["code"] in operation["error_codes"])
            return message
        result, body, name = message["result"], req["body"], req["operation"]
        self._check_registered(operation, "result_schema", result)
        if name == "engine.record_append":
            _require(result["record_kind"] == body["record_kind"])
            for field in ("campaign_id", "launch_id"):
                _require(result["attribution"][field] == req[field])
        elif name == "engine.artifact_begin":
            _require(result["purpose"] == body["purpose"] and result["artifact"] == body["artifact"])
        elif name == "engine.artifact_put_part":
            data = _part(body["content"])
            _require(result["upload_id"] == body["upload_id"] and result["offset"] == body["offset"])
            _require(result["raw_length"] == len(data) and result["next_offset"] == body["offset"] + len(data))
        elif name == "engine.artifact_commit":
            _require(result["upload_id"] == body["upload_id"])
            _conclusion_artifact(result)
        elif name == "engine.attempt_execute":
            if "request_id" in result:
                _require(result["request_id"] == req["operation_id"])
            if "attempt_id" in result:
                _require(result["attempt_id"] == body["attempt_id"])
        elif name == "engine.injection_delete":
            for field in ("attempt_receipt_id", "action_id"):
                _require(result[field] == body[field])
        elif name == "engine.observation_read":
            data = _part(result["content"])
            for field in ("receipt_id", "entry_id", "offset"):
                _require(result[field] == body[field])
            _require(result["raw_length"] == len(data) and len(data) <= body["max_bytes"])
            if result["availability"] == "available":
                end, size = result["offset"] + len(data), result["artifact"]["size_bytes"]
                _require(end <= size and result["eof"] == (end == size) and (len(data) > 0 or end == size))
        elif name in ("engine.snapshot_request", "engine.snapshot_inspect", "engine.restore_request"):
            snapshot = result["snapshot"]
            _metadata(snapshot, req["campaign_id"])
            if name == "engine.snapshot_request":
                for field in ("label", "description"):
                    _require(snapshot[field] == body.get(field, ""))
            else:
                for field in ("source_session", "checkpoint_id"):
                    _require(snapshot[field] == body[field])
            if name == "engine.restore_request":
                # Original effect binding and duplicate handling require the ledger.
                _require(result["run_revision"] > result["previous_run_revision"])
        elif name == "engine.snapshot_list":
            self._check_list(body, result, req["campaign_id"])
        elif name == "engine.request_stop":
            _require(result["conclusion_state"] == body["conclusion"]["state"])
        return message

    @staticmethod
    def _check_list(body, result, campaign):
        offset, limit = body.get("offset", 0), body.get("limit", 50)
        items = result["snapshots"]
        _require(result["campaign_id"] == campaign and result["offset"] == offset and len(items) <= limit)
        seen = set()
        for snapshot in items:
            _metadata(snapshot, campaign)
            key = (snapshot["source_session"], snapshot["checkpoint_id"])
            _require(key not in seen)
            seen.add(key)
            if "source_session" in body:
                _require(snapshot["source_session"] == body["source_session"])
        end, total = offset + len(items), result["total"]
        _require(not items or end <= total)
        if end < total:
            _require(bool(items) and result.get("next_offset") == end)
        else:
            _require("next_offset" not in result)
