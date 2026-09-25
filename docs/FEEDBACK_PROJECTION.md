# Native feedback projection

`internal/feedback` implements the agreed profile/selection translation, strict
native receipt validation, typed projection and receipt-scoped byte reads. All 17
shared translation vectors run against the production implementation.

The campaign's checked compatibility result supplies native/effective profiles
and permitted kinds. An empty intersection omits native selection and skips native
collection. Other selections retain the native profile and explicitly name only
the selected permitted kinds.

`VerifyView` checks the native typed hash, original campaign/session/attempt/turn,
context digest, session revision, capture window and closed feedback vocabulary.
Native JSON integer and hash rules remain separate from shared JCS rules.
`Assembly` verifies bounded sequential chunks and the complete artifact hash
before making bytes available.

`Project` builds new category summaries and opaque entry handles. It ignores raw
aliases, normalizes diagnostic facts, maps known injections to submitted action
IDs and substitutes opaque detector handles. Native metadata, errors and reasons
are not forwarded as arbitrary strings. Missing content, byte ceilings and
truncated diagnostic JSON remain explicit limitations. The caller supplies an
aggregate retention allowance; the projector never expands it.

`Receipt.Read` requires active channel admission, the same campaign, a known
receipt/entry and currently permitted kind. It returns bounded byte chunks from
the frozen projection, with explicit availability, truncation and EOF. Target
restore does not alter its original source. Worker/revision attribution is not an
access restriction.

Before publication, the broker must durably commit `RecordJSON` and all available
`Content` values, then construct the attempt result with `ManifestJSON`. This
package supplies immutable in-memory records; campaign receipt persistence and
service tool routing are subsequent integration work. It neither contacts the
target nor grants dispatch authority.

Native golden attempt/observation fixtures in `internal/interceptor/testdata`
were captured by the adjacent `capture_attempt_fixture_test.go.txt`, run through
a Go overlay against Interceptor's `internal/adaptive` package at commit
`567e6e0546a8f797c1673f90ba749e907b3dc56f`. The working tree had unrelated local
changes; the capture's adaptive contract/digest sources were unchanged. The
fixture includes a native event sequence above the shared safe-integer limit.
The overlay creates no Interceptor repository changes.
