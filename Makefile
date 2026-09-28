PYTHON ?= .venv/bin/python

.PHONY: setup test generate test-integration

setup:
	python3 -m venv .venv
	$(PYTHON) -m pip install -r contracts/python/requirements.lock
	go mod download

# Requires the sibling Attack Harness source checkout and make setup.
test-integration:
	OPERATOR_HARNESS_INTEGRATION=1 OPERATOR_PYTHON_PEER_TEST=1 go test -count=1 -timeout 5m ./internal/preparation ./internal/transport

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
	PYTHONPATH=contracts/python $(PYTHON) -m unittest discover -s contracts/python/tests -v
	$(PYTHON) schemas/validate_cleanup_fixtures.py
	$(PYTHON) schemas/validate_feedback_fixtures.py
	$(PYTHON) schemas/validate_capability_fixtures.py
