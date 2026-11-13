# Discoverable injection target fixture

Synthetic native capability bytes captured from Interceptor source
`582f7fb310a2ed0cf0ffd59223dfda1cdd608e54` using
`internal/capabilities/testdata/discovery_export_test.go.txt` in a disposable
Interceptor checkout. No live models, credentials, Docker or application calls
are involved. The export includes restricted/unrestricted/partly restricted
attacker sinks, static and HEAD-only sinks, and two identity issuers.

`native-digest-input.json` is the actual Go serialization with an empty digest.
`public-capabilities.json` is the expected verified projection, produced with
`schemas/capability_fixture_support.py:project`. Tests compare both with Operator
and validate the native response against the approved OpenAPI contract. Shared
compact-context fixtures retain the endpoint details behind digest-bound references.
