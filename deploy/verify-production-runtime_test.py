import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location(
    "verifier", Path(__file__).with_name("verify-production-runtime.py")
)
verifier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verifier)


class RuntimeVerificationTests(unittest.TestCase):
    def setUp(self):
        self.contract = verifier.load_contract(
            Path(__file__).with_name("production-runtime-contract.json")
        )
        self.web = {
            "metadata": {"name": "acuity-web", "annotations": {
                "run.googleapis.com/minScale": "1",
                "run.googleapis.com/maxScale": "2",
            }},
            "spec": {"template": {"spec": {
                "containerConcurrency": 40,
                "containers": [{"env": [{"name": "AUTH_DB_POOL_MAX", "value": "1"}]}],
            }}},
        }
        self.worker = {
            "metadata": {"name": "acuity-worker"},
            "spec": {"template": {
                "scaling": {"manualInstanceCount": "1"},
                "spec": {
                    "containerConcurrency": 0,
                    "containers": [{"env": [{"name": "DATABASE_POOL_MAX", "value": "2"}]}],
                },
            }},
        }

    def verify(self, name, live):
        contract = {"runtimes": [runtime for runtime in self.contract["runtimes"]
                                 if runtime["name"] == name]}
        with patch.object(verifier.subprocess, "run") as run:
            run.return_value.stdout = json.dumps(live)
            verifier.verify(contract, "synthetic-project", "us-east1")
            kind = "worker-pools" if name == "worker" else "services"
            self.assertEqual(run.call_args.args[0], [
                "gcloud", "run", kind, "describe", f"acuity-{name}",
                "--project", "synthetic-project", "--region", "us-east1", "--format", "json",
            ])

    def test_supported_service_shapes(self):
        self.verify("web", self.web)
        for location in ("spec", "root"):
            with self.subTest(location=location):
                live = copy.deepcopy(self.web)
                live["metadata"]["annotations"] = {}
                parent = live["spec"] if location == "spec" else live
                parent["scaling"] = {"minInstanceCount": 1, "maxInstanceCount": 2}
                template = live["spec"]["template"].pop("spec")
                template["maxInstanceRequestConcurrency"] = template.pop("containerConcurrency")
                live["spec"]["template"].update(template)
                self.verify("web", live)

    def test_supported_worker_shapes(self):
        self.verify("worker", self.worker)
        for location in ("root", "spec", "annotations"):
            with self.subTest(location=location):
                live = copy.deepcopy(self.worker)
                del live["spec"]["template"]["scaling"]
                if location == "annotations":
                    live["metadata"]["annotations"] = {"run.googleapis.com/manualInstanceCount": "1"}
                else:
                    parent = live if location == "root" else live["spec"]
                    parent["scaling"] = {"manualInstanceCount": 1}
                self.verify("worker", live)

    def test_every_capacity_dimension_fails_closed(self):
        for name, fixture in (("web", self.web), ("worker", self.worker)):
            for key in ("minimumInstances", "maximumInstances", "concurrency", "poolMaximum"):
                with self.subTest(name=name, key=key):
                    contract = copy.deepcopy(self.contract)
                    runtime = next(runtime for runtime in contract["runtimes"] if runtime["name"] == name)
                    runtime[key] += 1
                    with patch.object(verifier, "describe", return_value=fixture):
                        with self.assertRaisesRegex(ValueError, f"runtime contract drift: {key}"):
                            verifier.verify({"runtimes": [runtime]}, "synthetic-project", "us-east1")

    def test_revision_limits_do_not_satisfy_service_limits(self):
        self.web["metadata"]["annotations"] = {}
        self.web["spec"]["template"]["metadata"] = {"annotations": {
            "autoscaling.knative.dev/minScale": "1", "autoscaling.knative.dev/maxScale": "2",
        }}
        with self.assertRaisesRegex(ValueError, "minimumInstances expected 1, got missing"):
            self.verify("web", self.web)

    def test_missing_fields_and_wrong_runtime_fail_closed(self):
        for mutation, error in (
            (lambda live: live["metadata"].update(name="different-service"), "did not resolve exactly"),
            (lambda live: live["spec"]["template"]["spec"].pop("containers"), "poolMaximum.*missing"),
            (lambda live: live["spec"]["template"]["spec"].pop("containerConcurrency"), "concurrency.*missing"),
        ):
            live = copy.deepcopy(self.web)
            mutation(live)
            with self.subTest(error=error), self.assertRaisesRegex(ValueError, error):
                self.verify("web", live)

    def test_invalid_numbers_are_missing_and_zero_is_valid(self):
        for value in (None, True, False, "1x", "", 1.5, {}, [], -float("inf")):
            with self.subTest(value=value), self.assertRaisesRegex(ValueError, "got missing"):
                verifier.assert_value("acuity-web", "poolMaximum", 1, value)
        for value in (0, 0.0, "0"):
            verifier.assert_value("acuity-worker", "concurrency", 0, value)

    def test_failed_and_malformed_cloud_response_fail_closed(self):
        with patch.object(verifier.subprocess, "run", side_effect=subprocess.CalledProcessError(1, "gcloud")):
            with self.assertRaises(subprocess.CalledProcessError):
                verifier.verify(self.contract, "synthetic-project", "us-east1")
        with patch.object(verifier.subprocess, "run") as run:
            run.return_value.stdout = "not json"
            with self.assertRaises(ValueError):
                verifier.verify(self.contract, "synthetic-project", "us-east1")


if __name__ == "__main__":
    unittest.main()
