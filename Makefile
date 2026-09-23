PYTHON ?= .venv/bin/python

.PHONY: setup test generate

setup:
	python3 -m venv .venv
	$(PYTHON) -m pip install -r contracts/python/requirements.lock
	go mod download

generate:
	$(PYTHON) scripts/generate_envelopes.py
	$(PYTHON) scripts/generate_schema_ids.py

test:
	$(PYTHON) scripts/generate_envelopes.py --check
	$(PYTHON) scripts/generate_schema_ids.py --check
	go test ./...
	PYTHONPATH=contracts/python $(PYTHON) -m unittest discover -s contracts/python/tests -v
	$(PYTHON) schemas/validate_cleanup_fixtures.py
	$(PYTHON) schemas/validate_feedback_fixtures.py
	$(PYTHON) schemas/validate_capability_fixtures.py
