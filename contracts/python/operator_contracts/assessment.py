"""Bounded guest assessments and consistency checks for committed completion."""
import hashlib

from .validation import ContractError, CONTROL_LIMIT, MAX_SAFE_INTEGER
from .schema_ids import CONCLUSION_BINDING_SCHEMA, ENGINE_CONCLUSION_SCHEMA, ENGINE_PIPE_RESPONSE_SCHEMA

CONCLUSION_LIMIT = 1 << 20


def _require(condition):
    if not condition:
        raise ContractError("contract message consistency check failed")


def _ranges(value):
    if isinstance(value, dict):
        if "range" in value:
            span = value["range"]
            _require(span["offset"] + span["length"] <= MAX_SAFE_INTEGER)
        for child in value.values():
            _ranges(child)
    elif isinstance(value, list):
        for child in value:
            _ranges(child)


def _hypothesis(value):
    _require(value["provenance"].get("parent_hypothesis_id") != value["hypothesis_id"])


def _observations_linked(value):
    known = set(value["attempt_receipt_refs"])
    _require(all(item["attempt_receipt_id"] in known for item in value["observation_refs"]))


def check_record(body):
    record = body["record"]
    _ranges(record)
    if body["record_kind"] == "hypothesis":
        _hypothesis(record)
    elif body["record_kind"] == "progress":
        _observations_linked(record)
    elif body["record_kind"] == "lineage":
        _require(record.get("parent_attempt_receipt_id") != record["attempt_receipt_id"])


def validate_conclusion(protocol, raw: bytes):
    """Check content, not external receipt membership or truth of claimed effects."""
    conclusion = protocol._catalog.validate(ENGINE_CONCLUSION_SCHEMA, raw, CONCLUSION_LIMIT)
    _ranges(conclusion)
    for field, identifier in (("objectives", "objective_id"), ("hypotheses", "hypothesis_id"),
                              ("claims", "claim_id"), ("uncertainties", "gap_id")):
        ids = [entry[identifier] for entry in conclusion[field]]
        _require(len(ids) == len(set(ids)))
    gaps = {gap["gap_id"] for gap in conclusion["uncertainties"]}
    for gap in conclusion["uncertainties"]:
        _require(gap["kind"] != "coverage-limit" or conclusion["status"] != "completed")
    for claim in conclusion["claims"]:
        _observations_linked(claim)
        _require(set(claim["uncertainty_refs"]) <= gaps)
    for hypothesis in conclusion["hypotheses"]:
        _hypothesis(hypothesis)
    return conclusion


def validate_completion(protocol, *, conclusion: bytes, binding: bytes,
                        artifact_commit_response: bytes, record_request: bytes,
                        record_response: bytes, stop_request: bytes):
    """Validate retained host records. Does not resolve receipts or persist stop."""
    content = validate_conclusion(protocol, conclusion)
    expected = protocol._catalog.validate(CONCLUSION_BINDING_SCHEMA, binding, CONTROL_LIMIT)
    _require(content["binding"] == expected)
    request = protocol.validate_request(record_request)
    _require(request["operation"] == "engine.record_append")
    body = request["body"]
    _require(body["record_kind"] == "conclusion")
    response = protocol.validate_response(record_request, record_response)
    _require("result" in response)
    recorded = response["result"]
    stop = protocol.validate_request(stop_request)
    _require(stop["operation"] == "engine.request_stop")
    for message in (request, stop, recorded["attribution"]):
        for key in ("campaign_id", "launch_id", "run_revision"):
            _require(message[key] == expected[key])
    commit = protocol._catalog.validate(ENGINE_PIPE_RESPONSE_SCHEMA, artifact_commit_response)
    _require(commit["operation"] == "engine.artifact_commit")
    for key in ("campaign_id", "launch_id"):
        _require(commit[key] == expected[key])
    _require("result" in commit and commit["result"]["purpose"] == "conclusion")
    artifact = commit["result"]
    descriptor = artifact["artifact"]
    _require(descriptor["digest"] == "sha256:" + hashlib.sha256(conclusion).hexdigest())
    _require(descriptor["size_bytes"] == len(conclusion) and descriptor["media_type"] == "application/json")
    _require(stop["body"]["conclusion"]["state"] == "committed")
    record = body["record"]
    stop_conclusion = stop["body"]["conclusion"]
    _require(record["artifact_receipt"] == artifact["artifact_receipt"] == stop_conclusion["artifact_receipt"])
    _require(stop_conclusion["record_receipt"] == recorded["receipt_id"])
    _require(record["finish_reason"] == content["finish_reason"] == stop["body"]["finish_reason"])
