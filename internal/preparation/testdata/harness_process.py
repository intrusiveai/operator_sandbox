"""Test-only launch adapter: real entrypoint/inputs/loop, temporary host paths.

This does not qualify Docker or Linux seccomp. No test switches are installed in
the production harness: only this separate process supplies path relocation and
a confinement callback, before invoking the unchanged production main().
"""
import functools
import json
import os
from pathlib import Path
import sys
import types

config = json.loads(Path(sys.argv[1]).read_text())
sys.path[:0] = [config["harness"] + "/src",
               config["package"] + "/source/contracts/python"]
from attack_harness import entrypoint
from attack_harness.fifo import FIFO
from attack_harness.spool import Spool
from attack_harness.startup import Startup

entrypoint.CONTRACT_ROOT = config["package"]
entrypoint.load_runtime_config = functools.partial(
    entrypoint.load_runtime_config, config["release"])
entrypoint.Startup = functools.partial(Startup, inputs_options={
    "manifests_root": config["tree"] + "/manifests",
    "input_root": config["tree"] + "/input",
    "skills_root": config["tree"] + "/customer-skills",
})
os.environ["OPERATOR_TRANSPORT"] = config["transport"]
entrypoint.transport_from_environment = lambda protocol: (
    FIFO(protocol, config["ipc"]) if config["transport"] == "fifo"
    else Spool(protocol, config["ipc"]))
sys.modules["_confinement"] = types.SimpleNamespace(install=lambda: None)
sys.exit(entrypoint.main())
