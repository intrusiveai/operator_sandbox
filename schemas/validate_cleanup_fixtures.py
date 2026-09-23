"""Validate cleanup wire fixtures; requires jsonschema (Draft 2020-12)."""
import json
from pathlib import Path
from jsonschema import Draft202012Validator

root = Path(__file__).resolve().parent
catalog = json.loads((root / "catalog.json").read_text())
validators = {}
for uri, name in catalog.items():
    schema = json.loads((root / name).read_text())
    assert schema["$id"] == uri, name
    Draft202012Validator.check_schema(schema)
    validators[uri] = Draft202012Validator(schema)

vectors = json.loads((root / "fixtures/injection-cleanup.json").read_text())
for case in vectors:
    errors = list(validators[case["schema"]].iter_errors(case["instance"]))
    assert (not errors) == case["valid"], (case["name"], errors)
print(f"{len(vectors)} cleanup fixtures passed; {len(catalog)} schema definitions checked")
