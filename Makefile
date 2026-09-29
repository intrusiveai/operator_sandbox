PYTHON ?= .venv/bin/python

.PHONY: setup test generate test-integration release-candidate

setup:
	python3 -m venv .venv
	$(PYTHON) -m pip install -r contracts/python/requirements.lock
	go mod download

# Requires the sibling Attack Harness source checkout and make setup.
test-integration:
	OPERATOR_TEST_PYTHON="$(abspath $(PYTHON))" OPERATOR_HARNESS_INTEGRATION=1 OPERATOR_PYTHON_PEER_TEST=1 go test -count=1 -timeout 10m ./internal/preparation ./internal/transport

generate:
	go run ./scripts/generate_path_unicode.go
	$(PYTHON) scripts/generate_model_schemas.py
	$(PYTHON) scripts/generate_tool_catalog.py
	$(PYTHON) scripts/generate_envelopes.py
	$(PYTHON) scripts/generate_schema_ids.py

test:
	go run ./scripts/generate_path_unicode.go -check
	$(PYTHON) scripts/generate_model_schemas.py --check
	$(PYTHON) scripts/generate_tool_catalog.py --check
	$(PYTHON) scripts/generate_envelopes.py --check
	$(PYTHON) scripts/generate_schema_ids.py --check
	go test ./...
	python3 -m unittest discover -s scripts/tests
	PYTHONPATH=contracts/python $(PYTHON) -m unittest discover -s contracts/python/tests -v
	$(PYTHON) schemas/validate_cleanup_fixtures.py
	$(PYTHON) schemas/validate_feedback_fixtures.py
	$(PYTHON) schemas/validate_capability_fixtures.py

# Build-only candidates; explicit publisher signing and qualification are separate.
release-candidate:
	python3 scripts/build_host_release.py --version $(VERSION) --output $(OUTPUT)
