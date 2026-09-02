#!/usr/bin/env python3
"""Check the deployed capacity contract using the Cloud SDK image's Python."""

import json
import re
import subprocess
import sys


def integer(value):
    if type(value) is int or (type(value) is float and value.is_integer()):
        return int(value)
    if isinstance(value, str) and re.fullmatch(r"[0-9]+", value):
        return int(value)
    return None


def field(value, *keys):
    for key in keys:
        if not isinstance(value, dict):
            return None
        value = value.get(key)
    return value


def first(*values):
    return next((value for value in values if value is not None), None)


def load_contract(path):
    with open(path, encoding="utf-8") as source:
        contract = json.load(source)
    runtimes = field(contract, "runtimes")
    if not isinstance(runtimes, list) or not runtimes:
        raise ValueError("production runtime contract must define runtimes")
    names = set()
    for runtime in runtimes:
        name = field(runtime, "name")
        if not isinstance(name, str) or not name or name in names:
            raise ValueError("production runtime contract must define unique runtime names")
        names.add(name)
        if field(runtime, "kind") not in ("service", "worker-pool"):
            raise ValueError(f"acuity-{name} has unsupported kind {field(runtime, 'kind')}")
        for key in ("concurrency", "minimumInstances", "maximumInstances", "poolMaximum"):
            value = field(runtime, key)
            if type(value) is not int or value < 0:
                raise ValueError(f"acuity-{name} contract must define a nonnegative integer {key}")
    return contract


def describe(kind, name, project, region):
    result = subprocess.run(
        ["gcloud", "run", kind, "describe", name,
         "--project", project, "--region", region, "--format", "json"],
        stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, text=True, check=True,
    )
    return json.loads(result.stdout)


def assert_value(name, key, expected, value):
    actual = integer(value)
    if actual != expected:
        raise ValueError(
            f"{name} runtime contract drift: {key} expected {expected}, "
            f"got {actual if actual is not None else 'missing'}"
        )


def template_spec(live):
    return first(field(live, "spec", "template", "spec"), field(live, "spec", "template"))


def pool_value(live, environment_name):
    containers = field(template_spec(live), "containers")
    environment = field(containers[0], "env") if isinstance(containers, list) and containers else None
    if isinstance(environment, list):
        return next((field(entry, "value") for entry in environment
                     if field(entry, "name") == environment_name), None)
    return None


def verify(contract, project, region):
    for runtime in contract["runtimes"]:
        name = f"acuity-{runtime['name']}"
        kind = "services" if runtime["kind"] == "service" else "worker-pools"
        live = describe(kind, name, project, region)
        if field(live, "metadata", "name") != name:
            raise ValueError(f"{name} did not resolve exactly")
        if runtime["kind"] == "service":
            scaling = first(field(live, "spec", "scaling"), field(live, "scaling"), {})
            values = {
                "concurrency": first(
                    field(template_spec(live), "containerConcurrency"),
                    field(live, "spec", "template", "maxInstanceRequestConcurrency")),
                "minimumInstances": first(
                    field(scaling, "minInstanceCount"),
                    field(live, "metadata", "annotations", "run.googleapis.com/minScale")),
                "maximumInstances": first(
                    field(scaling, "maxInstanceCount"),
                    field(live, "metadata", "annotations", "run.googleapis.com/maxScale")),
                "poolMaximum": pool_value(
                    live, "AUTH_DB_POOL_MAX" if runtime["name"] == "web" else "DATABASE_POOL_MAX"),
            }
        else:
            instances = first(
                field(live, "scaling", "manualInstanceCount"),
                field(live, "spec", "scaling", "manualInstanceCount"),
                field(live, "spec", "template", "scaling", "manualInstanceCount"),
                field(live, "metadata", "annotations", "run.googleapis.com/manualInstanceCount"),
            )
            values = {
                "minimumInstances": instances,
                "maximumInstances": instances,
                "concurrency": field(template_spec(live), "containerConcurrency"),
                "poolMaximum": pool_value(live, "DATABASE_POOL_MAX"),
            }
        for key, value in values.items():
            assert_value(name, key, runtime[key], value)


def main(arguments):
    if len(arguments) == 2 and arguments[1] == "--check-contract":
        load_contract(arguments[0])
    elif len(arguments) == 3:
        verify(load_contract(arguments[0]), arguments[1], arguments[2])
    else:
        raise ValueError("usage: verify-production-runtime.py CONTRACT PROJECT REGION | CONTRACT --check-contract")


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)
