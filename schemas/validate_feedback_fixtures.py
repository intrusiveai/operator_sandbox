"""Check feedback translation vectors; this is not a production adapter test.

Requires jsonschema (Draft 2020-12). Nonempty collection fixtures assume empty
native categories. Runtime projection and receipt-read tests remain required.
"""
import json
from pathlib import Path

from jsonschema import Draft202012Validator

ROOT = Path(__file__).resolve().parent
KINDS = ["target_output", "operation_error", "injection_delivery", "oracle_outcome"]
PROFILES = {"black-box": KINDS[:1], "diagnostic": KINDS[:3], "oracle-assisted": KINDS}
ORDER = list(PROFILES)


def validator(name):
    schema = json.loads((ROOT / name).read_text())
    Draft202012Validator.check_schema(schema)
    return Draft202012Validator(schema)


selection_validator = validator("observation-selection.schema.json")
manifest_validator = validator("feedback-manifest.schema.json")
cases = json.loads((ROOT / "fixtures/feedback-translation.json").read_text())["cases"]
names = set()
combinations = set()
for case in cases:
    name, inputs, expected = case["name"], case["input"], case["expected"]
    assert name not in names, name
    names.add(name)
    native, requested = inputs["native_profile"], inputs["requested_profile"]
    combinations.add((native, requested))
    effective = min(
        (native, requested, inputs.get("host_profile_ceiling", "oracle-assisted")),
        key=ORDER.index,
    )
    allowed = [k for k in PROFILES[effective] if k in inputs.get("host_allowed_kinds", KINDS)]
    selection = inputs.get("observation_selection", {"mode": "all-permitted"})
    selection_validator.validate(selection)
    requested_kinds = selection.get("kinds", KINDS)
    selected = [k for k in allowed if k in requested_kinds]
    assert expected["native_attempt_profile"] == native, name
    assert expected["allowed_kinds"] == allowed, name
    assert expected["collect_native_feedback"] == bool(selected), name
    native_selection = expected["native_observation_selection"]
    if selected:
        selection_validator.validate(native_selection)
        assert native_selection == {"mode": "selected", "kinds": selected}, name
    else:
        assert native_selection is None, name
    feedback = expected["feedback"]
    manifest_validator.validate(feedback)
    assert feedback["profile"] == effective, name
    assert feedback["entries"] == [] and feedback["collection_state"] == "complete", name
    assert [c["kind"] for c in feedback["categories"]] == KINDS, name
    for category in feedback["categories"]:
        kind = category["kind"]
        if kind not in allowed:
            assert category == {"kind": kind, "state": "withheld", "reason": "effective_feedback_policy"}, name
        elif kind not in selected:
            assert category == {"kind": kind, "state": "not_requested", "reason": "selection"}, name
        else:
            assert category["state"] == "empty", name

assert combinations == {(n, r) for n in PROFILES for r in PROFILES}
print(f"{len(cases)} feedback translation fixtures passed; all 9 profile combinations covered")
