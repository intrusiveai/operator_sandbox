# Operator / Attack Harness process integration

Run `make setup` and `make test-integration` with the sibling `attack_harness`
checkout. `OPERATOR_TEST_HARNESS_SOURCE` MAY select another trusted source checkout.
The integration target MUST fail if the selected harness or Python dependencies
are unavailable. Ordinary `go test` MUST skip these explicitly opt-in cases.

The tests build a fresh contract package from the current Operator source, load
its exact digest on both ends, stage immutable inputs with the production launcher,
and exchange real bytes with a separate Python process. The Python process invokes
`attack_harness.entrypoint.main`, including contract/input validation, startup,
campaign composition and the adaptive loop. The Go side uses production bootstrap,
transport, campaign service and journal implementations.

The test-only Python launch adapter relocates fixed filesystem paths into private
temporary directories and supplies a no-op confinement callback. It MUST NOT be
included in a release image or enabled by production configuration. Local FIFO
tests also run on macOS to test wire behavior; they do not qualify the Linux
container profile. External model, native-target, Docker and image-approval
dependencies are controlled fixtures. Tests MUST NOT claim live provider, Docker,
seccomp, production release-service or native host qualification from these results.

## Startup coverage

`TestPythonProcessStartupInputs` covers both FIFO and spool with default,
replacement and ordered-extension prompts, passive references, empty/selected
skills and an input manifest larger than a control frame. The model receives the
exact staged prompt and selected skill instructions. The real Python loop reaches
an accepted stop acknowledgement. No fabricated guest startup transcript is used.
