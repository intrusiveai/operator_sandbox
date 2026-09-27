PYTHON ?= .venv/bin/python

.PHONY: setup test generate

setup:
	python3 -m venv .venv
	$(PYTHON) -m pip install -r contracts/python/requirements.lock
	go mod download

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
