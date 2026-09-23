"""Manifest construction and verification over explicitly supplied frozen bytes."""
import json
from .validation import ContractError, ORDINARY_LIMIT, decode
from .schema_ids import CONTRACT_PACKAGE_SCHEMA
from .startup import require, inventory_paths, folded
from .canonical import canonicalize, canonical_digest, raw_digest

PACKAGE_MANIFEST_LIMIT = 8 << 20
PACKAGE_FILE_LIMIT = 16 << 20
PACKAGE_CONTENT_LIMIT = 128 << 20
REQUIRED_PROFILES = frozenset(('jcs-v1', 'manifest-paths-v1', 'harness-loop-v1'))


def validate_package_manifest(protocol, raw):
    m = protocol._catalog.validate(CONTRACT_PACKAGE_SCHEMA, raw, PACKAGE_MANIFEST_LIMIT)
    inventory_paths(m['files'])
    entries = {e['path']: e for e in m['files']}
    require(sum(e['size_bytes'] for e in m['files']) <= PACKAGE_CONTENT_LIMIT)
    require('catalog.json' in entries and 'operations.json' in entries)
    for name in entries:
        require(folded(name.split('/', 1)[0]) not in ('package.json', 'signatures'))
    previous, profiles = '', set()
    for profile in m['semantic_profiles']:
        identifier = profile['profile_id']
        require(identifier > previous)
        previous = identifier
        entry = entries.get(profile['path'])
        require(entry is not None and entry['size_bytes'] > 0 and entry['digest'] == profile['digest'])
        profiles.add(identifier)
    require(REQUIRED_PROFILES <= profiles)
    return m


def verify_package(protocol, manifest, files, expected):
    m = validate_package_manifest(protocol, manifest)
    require(expected == {'package_version': m['package_version'], 'package_digest': canonical_digest(manifest, PACKAGE_MANIFEST_LIMIT)})
    require(len(files) == len(m['files']))
    for entry in m['files']:
        raw = files.get(entry['path'])
        require(type(raw) is bytes and len(raw) == entry['size_bytes'] and raw_digest(raw) == entry['digest'])
    for key, name in (('catalog', 'catalog.json'), ('operations', 'operations.json')):
        require(m[key]['object_digest'] == canonical_digest(files[name]))
    catalog = decode(files['catalog.json'])
    require(type(catalog) is dict and bool(catalog))
    seen = set()
    for name in catalog.values():
        require(type(name) is str and '/' not in name and '\\' not in name and name.endswith('.schema.json') and name not in seen and name in files)
        seen.add(name)


def _bounded_files(files):
    require(len(files) <= 4096)
    total = 0
    for name, raw in files.items():
        require(type(name) is str)
        try:
            require(len(name.encode('utf-8')) <= 1024)
        except UnicodeError:
            raise ContractError('contract message consistency check failed') from None
        require(type(raw) is bytes and len(raw) <= PACKAGE_FILE_LIMIT)
        total += len(raw)
        require(total <= PACKAGE_CONTENT_LIMIT)


def build_package_manifest(protocol, version, files, profiles):
    _bounded_files(files)
    require(type(version) is str and len(version) <= 128 and version.isascii())
    require(2 <= len(files) and len(profiles) <= 64)
    require(all(type(k) is str and len(k) <= 128 and k.isascii() and type(v) is str for k, v in profiles.items()))
    require('catalog.json' in files and 'operations.json' in files)
    require(all(path in files for path in profiles.values()))
    m = {'api_version': 'operator.dev/contract-package/v1alpha1', 'package_version': version,
         'files': [{'path': path, 'size_bytes': len(files[path]), 'digest': raw_digest(files[path])} for path in sorted(files)],
         'catalog': {'path': 'catalog.json', 'object_digest': canonical_digest(files['catalog.json'])},
         'operations': {'path': 'operations.json', 'object_digest': canonical_digest(files['operations.json'])},
         'semantic_profiles': [{'profile_id': identifier, 'path': profiles[identifier], 'digest': raw_digest(files[profiles[identifier]])} for identifier in sorted(profiles)]}
    raw = json.dumps(m, ensure_ascii=False, separators=(',', ':')).encode('utf-8')
    validate_package_manifest(protocol, raw)
    raw = canonicalize(raw, PACKAGE_MANIFEST_LIMIT)
    pin = {'package_version': version, 'package_digest': raw_digest(raw)}
    verify_package(protocol, raw, files, pin)
    return raw, pin


def load_verified_protocol(protocol, manifest, files, expected):
    # Snapshot the mapping once. Values must be immutable bytes, not live buffers.
    frozen = dict(files)
    _bounded_files(frozen)
    verify_package(protocol, manifest, frozen, expected)
    loaded = type(protocol)._from_resources(frozen)
    loaded._package_identity = dict(expected)
    return loaded
