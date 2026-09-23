"""Startup transcript and manifest metadata validation, without runtime I/O."""
import hashlib
import unicodedata
from . import path_unicode

from .validation import ContractError, CONTROL_LIMIT
from .schema_ids import (
    ENGINE_HOST_CONTROL_SCHEMA, ENGINE_GUEST_CONTROL_SCHEMA,
    INPUT_TREE_MANIFEST_SCHEMA, SKILL_MANIFEST_SCHEMA, SKILL_SET_MANIFEST_SCHEMA,
)


def require(value):
    if not value:
        raise ContractError("contract message consistency check failed")


def validate_control(protocol, direction, raw):
    schemas = {"host": ENGINE_HOST_CONTROL_SCHEMA, "guest": ENGINE_GUEST_CONTROL_SCHEMA}
    require(direction in schemas)
    message = protocol._catalog.validate(schemas[direction], raw, CONTROL_LIMIT)
    body, kind = message["body"], message["kind"]
    if kind in ("bootstrap", "confinement_ready"):
        expected = "spool" if body["host_platform"].startswith("darwin/") else "fifo"
        require(body["transport"] == expected)
    elif kind in ("initialize", "initialized", "admission_open"):
        binding = body["binding"]
        require(all(message[key] == binding[key] for key in ("campaign_id", "launch_id", "run_revision")))
        if kind != "admission_open":
            require(body["input_tree"]["digest"] == binding["input_tree_digest"])
            require(body["skill_set"]["digest"] == binding["skill_set_digest"])
        else:
            names = body["operations"]
            require(names == sorted(set(names)) and all(name in protocol._operations for name in names))
            campaign, harness = body["limits"]["campaign"], body["limits"]["harness"]
            require(all(value <= campaign[key] for key, value in body["remaining_limits"].items()))
            require(campaign["model_turns"] <= harness["max_model_turns"])
            require(campaign["observation_bytes"] <= harness["max_read_bytes"])
    return message


def validate_startup(protocol, raw_messages):
    require(len(raw_messages) == 5)
    directions = ("host", "guest", "host", "guest", "host")
    kinds = ("bootstrap", "confinement_ready", "initialize", "initialized", "admission_open")
    seqs = (0, 0, 1, 1, 2)
    messages = []
    for direction, kind, seq, raw in zip(directions, kinds, seqs, raw_messages):
        message = validate_control(protocol, direction, raw)
        require(message["kind"] == kind and message["seq"] == seq)
        if messages:
            require(all(message[key] == messages[0][key] for key in ("campaign_id", "launch_id", "run_revision")))
        messages.append(message)
    bodies = [message["body"] for message in messages]
    require(all(value == bodies[0][key] for key, value in bodies[1].items()))
    require(all(bodies[2][key] == bodies[3][key] for key in ("input_tree", "skill_set", "binding", "engine_context_object_digest")))
    require(bodies[3]["contract"] == bodies[0]["contract"])
    require(bodies[4]["binding"] == bodies[2]["binding"])
    binding, contract, release = bodies[2]["binding"], bodies[0]["contract"], bodies[0]["release"]
    expected = {"contract_package_version": contract["version"], "contract_package_digest": contract["digest"],
                "image_digest": release["image_digest"], "release_record_digest": release["release_record_digest"]}
    require(all(binding[key] == value for key, value in expected.items()))


def folded(value):
    return unicodedata.normalize("NFC", path_unicode.casefold(value))


def inventory_paths(entries):
    previous, prefixes, files = "", {}, set()
    for entry in entries:
        path = entry["path"]
        require(len(path.encode("utf-8")) <= 1024 and "\\" not in path)
        require(unicodedata.normalize("NFC", path) == path and path > previous)
        require(all(ord(char) >= 32 and ord(char) != 127 and path_unicode.assigned(char) for char in path))
        # Match x/text's stream-safe NFC profile, including decompositions of
        # precomposed characters. Do not insert normalization characters silently.
        nonstarters = 0
        for char in unicodedata.normalize("NFD", path):
            nonstarters = nonstarters + 1 if unicodedata.combining(char) else 0
            require(nonstarters <= 30)
        parts = path.split("/")
        require(len(parts) <= 16 and all(part not in ("", ".", "..") for part in parts))
        previous = path
        for index in range(len(parts)):
            prefix = "/".join(parts[:index + 1])
            key = folded(prefix)
            if key in prefixes:
                require(prefixes[key] == prefix and key not in files and index != len(parts) - 1)
            prefixes[key] = prefix
            if index == len(parts) - 1:
                files.add(key)


def validate_input_tree(protocol, raw):
    manifest = protocol._catalog.validate(INPUT_TREE_MANIFEST_SCHEMA, raw, 8 << 20)
    entries = manifest["entries"]
    inventory_paths(entries)
    identifiers, roles, total = set(), {}, 0
    for entry in entries:
        require(entry["entry_id"] not in identifiers)
        identifiers.add(entry["entry_id"])
        role = entry["role"]
        roles[role] = roles.get(role, 0) + 1
        total += entry["size_bytes"]
        if role == "reference":
            require(entry["path"] == "artifacts/sha256-" + entry["digest"][7:])
    require(total <= 64 << 20 and all(roles.get(role) == 1 for role in ("engine-context", "scenario-bundle", "system-prompt")))
    return manifest


def validate_skill_manifest(protocol, raw):
    manifest = protocol._catalog.validate(SKILL_MANIFEST_SCHEMA, raw, 2 << 20)
    files = manifest["files"]
    inventory_paths(files)
    require(sum(file["size_bytes"] for file in files) <= 8 << 20)
    require(any(file["path"] == "SKILL.md" and file["media_type"] == "text/markdown" and file["size_bytes"] > 0 for file in files))
    return manifest


def validate_skill_set(protocol, raw):
    manifest = protocol._catalog.validate(SKILL_SET_MANIFEST_SCHEMA, raw, CONTROL_LIMIT)
    previous, seen = "", set()
    for index, entry in enumerate(manifest["skills"]):
        identifier, key = entry["skill_id"], folded(entry["skill_id"])
        require(identifier > previous and key not in seen and entry["manifest"]["slot"] == index)
        previous = identifier
        seen.add(key)
    return manifest


def validate_manifest_set(protocol, tree, skill_set, skills):
    validate_input_tree(protocol, tree)
    manifest = validate_skill_set(protocol, skill_set)
    entries = manifest["skills"]
    require(len(entries) == len(skills))
    encoded, content = len(tree) + len(skill_set), 0
    for entry, raw in zip(entries, skills):
        encoded += len(raw)
        if encoded > 40 << 20:
            raise ContractError("contract encoding limit exceeded")
        skill = validate_skill_manifest(protocol, raw)
        descriptor = entry["manifest"]
        require(skill["skill_id"] == entry["skill_id"] and descriptor["size_bytes"] == len(raw))
        require(descriptor["digest"] == "sha256:" + hashlib.sha256(raw).hexdigest())
        content += sum(file["size_bytes"] for file in skill["files"])
    require(content <= 64 << 20)


def validate_startup_inputs(protocol, messages, tree, skill_set, skills):
    validate_startup(protocol, messages)
    validate_manifest_set(protocol, tree, skill_set, skills)
    body = validate_control(protocol, "host", messages[2])["body"]
    for key, raw in (("input_tree", tree), ("skill_set", skill_set)):
        descriptor = body[key]
        require(descriptor["size_bytes"] == len(raw))
        require(descriptor["digest"] == "sha256:" + hashlib.sha256(raw).hexdigest())
    manifest = validate_input_tree(protocol, tree)
    for entry in manifest["entries"]:
        field = {"engine-context": "engine_context_digest", "system-prompt": "prompt_digest"}.get(entry["role"])
        if field:
            require(entry["digest"] == body["binding"][field])
