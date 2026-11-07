PYTHON ?= .venv/bin/python

.PHONY: setup test test-pr check-generated generate test-integration release-candidate test-release-publisher

setup:
	python3 -m venv .venv
	$(PYTHON) -m pip install -r contracts/python/requirements.lock
	go mod download

# Requires the sibling Attack Harness source checkout and make setup.
test-integration:
	OPERATOR_TEST_PYTHON="$(abspath $(PYTHON))" OPERATOR_HARNESS_INTEGRATION=1 OPERATOR_PYTHON_PEER_TEST=1 go test -count=1 -timeout 20m -run '^Test(Python|Harness)' ./internal/preparation ./internal/transport

generate:
	go run ./scripts/generate_path_unicode.go
	$(PYTHON) scripts/generate_model_schemas.py
	$(PYTHON) scripts/generate_tool_catalog.py
	$(PYTHON) scripts/generate_envelopes.py
	$(PYTHON) scripts/generate_schema_ids.py
	$(PYTHON) scripts/generate_asyncapi.py

check-generated:
	go run ./scripts/generate_path_unicode.go -check
	$(PYTHON) scripts/generate_model_schemas.py --check
	$(PYTHON) scripts/generate_tool_catalog.py --check
	$(PYTHON) scripts/generate_envelopes.py --check
	$(PYTHON) scripts/generate_schema_ids.py --check
	$(PYTHON) scripts/generate_asyncapi.py --check
	$(PYTHON) scripts/check_harness_contract.py --document-only

test: check-generated
	OPERATOR_TEST_PYTHON="$(abspath $(PYTHON))" OPERATOR_CONTROLLED_PEER_TEST=1 go test ./...
	$(PYTHON) -m unittest discover -s scripts/tests
	PYTHONPATH=contracts/python $(PYTHON) -m unittest discover -s contracts/python/tests -v
	$(PYTHON) schemas/validate_cleanup_fixtures.py
	$(PYTHON) schemas/validate_feedback_fixtures.py
	$(PYTHON) schemas/validate_capability_fixtures.py
	$(MAKE) test-release-publisher

# Fast PR checks. Candidate pushes retain the complete test/build gate below CI.
test-pr: check-generated
	go build ./...
	go vet ./...
	go test ./contracts ./internal/hostconfig ./internal/targetprofile ./internal/modelprovider
	$(PYTHON) -m unittest discover -s scripts/tests
	PYTHONPATH=contracts/python $(PYTHON) -m unittest discover -s contracts/python/tests
	$(PYTHON) schemas/validate_cleanup_fixtures.py
	$(PYTHON) schemas/validate_feedback_fixtures.py
	$(PYTHON) schemas/validate_capability_fixtures.py
	$(MAKE) test-release-publisher

# Catalog-only source export: no private modules or runtime toolchain downloads.
test-release-publisher:
	cd tools/release-publisher && GOWORK=off GOPROXY=off GOTOOLCHAIN=local go test -race ./...
	cd tools/release-publisher && GOWORK=off GOPROXY=off GOTOOLCHAIN=local go vet ./...
	cd tools/release-publisher && GOWORK=off GOPROXY=off GOTOOLCHAIN=local $(abspath $(PYTHON)) -m unittest discover -s scripts/tests

# Build-only candidates; publication and qualification are separate.
release-candidate:
	python3 scripts/build_host_release.py --version $(VERSION) --output $(OUTPUT)

# Functional conformance uses controlled peers, not a pinned sibling product.
test-contract:
	$(PYTHON) scripts/check_harness_contract.py --output dist/contract-conformance.json
