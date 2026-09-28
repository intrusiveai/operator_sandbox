# Operator / Attack Harness process integration

Run `make setup` and `make test-integration` with the sibling `attack_harness`
checkout. `OPERATOR_TEST_HARNESS_SOURCE` MAY select another trusted source checkout.
`make test-integration PYTHON=/absolute/python` selects the test interpreter;
direct Go invocations MAY set `OPERATOR_TEST_PYTHON` to the same absolute path.
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

## Campaign coverage

`TestPythonProcessCampaigns` exercises objectives-only, scenario-guided and
exploratory campaigns on both transports. A controlled model requests hypothesis
records, local reference reads, multipart artifact publication, two related
attempts, receipt-scoped feedback reads and a structured conclusion. Later model
responses depend on the actual earlier receipt IDs and feedback bytes. The tests
verify retained reports and accepted stop acknowledgement. The HTTPS variants use
the production HTTPS adapter and a local TLS server, preserving observer assurance.

The joined test discovered a release-identity disagreement: attempts MUST use the
compatibility-record digest in `generator.release_digest`, as defined by the shared
contract. Host and harness component fixtures alone had not detected that mismatch.

## Restore and interruption coverage

The restore process tests verify snapshot metadata/list/inspect, continued use of
the same Python process, a single replacement revision, correlated skipped results
for the rest of the model batch, retained-injection deletion through the original
receipt, and child-attempt lineage after restore. Lost restore replies MUST close
execution without a second restore or subsequent model turn.

Failure tests interrupt an in-flight model request with host cancellation,
harness process loss, control-lane termination and spool overflow. They MUST retain
the uncertain outcome, reject readmission and finalize without model replay.
`TestPythonProcessHostCrashRetainsUnknownWork` kills a separate Go host process
after a model intent is durable, independently kills its surviving Python process,
and verifies that repeated cleanup preserves the incomplete journal prefix.
Docker event delivery remains a fixture boundary; these tests do not qualify a
live daemon restart or native host interruption.

## History and reproducibility

`TestPythonProcessLargeHistoryCompaction` crosses the production 1 MiB conversation
threshold over both transports and checks that the next request suppresses tools,
retains the full source history, charges another model turn, and then resumes with
the objective and explicit summary provenance intact. Attack Harness's
`test_history_stress.py` exercises three 200-segment compaction cycles for each of
the five codecs. Synthetic model usage counts are fixtures, not tokenizer or live
provider qualification.

The [acceptance matrix](../../attack_harness/ACCEPTANCE_MATRIX.md) records exact
source and development contract identities. A package/image built from older pins
MUST NOT be described as having passed these tests.
