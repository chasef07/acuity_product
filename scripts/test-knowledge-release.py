#!/usr/bin/env python3
"""Synthetic receipt coverage; never contacts GitHub or production."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import sys
import unittest
from unittest.mock import patch, Mock

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('knowledge_release', Path(__file__).with_name('knowledge-release.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
COMMIT = 'a' * 40


class ReleaseTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.receipt()
        self.env = patch.dict(os.environ, GITHUB_RUN_ID='123')
        self.env.start()
        self.addCleanup(self.env.stop)

    def receipt(self, office='alpha', **changes):
        value = dict(applied=True, unchanged=False, activeRevisionVerified=True,
                     sourceGitCommit=COMMIT, provenance=f'git:{COMMIT}',
                     officeKey=office, revision={'id': f'synthetic-{office}'})
        value.update(changes)
        (self.root / f'{office}.json').write_text(json.dumps(value))

    def run_release(self):
        module.release(self.root, COMMIT, 'all', 'synthetic/repo')

    @patch.object(module, 'github', return_value=None)
    def test_changed_offices_only_and_product_latest_preserved(self, api):
        self.receipt('beta', applied=False, unchanged=True, provenance='git:' + 'b' * 40)
        (self.root / 'alpha-evaluation.json').write_text('{}')
        self.run_release()
        payload = api.call_args.args[2]
        self.assertEqual(payload['target_commitish'], COMMIT)
        self.assertEqual(payload['make_latest'], 'false')
        self.assertIn('synthetic-alpha', payload['body'])
        self.assertNotIn('synthetic-beta', payload['body'])

    @patch.object(module, 'github')
    def test_unchanged_older_revision_never_calls_github(self, api):
        self.receipt(applied=False, unchanged=True, provenance='git:' + 'b' * 40)
        self.run_release()
        api.assert_not_called()

    @patch.object(module, 'github', return_value=None)
    def test_retry_recovers_release_after_database_already_changed(self, api):
        self.receipt(applied=False, unchanged=True)
        self.run_release()
        self.assertEqual(api.call_count, 2)
        self.assertIn('synthetic-alpha', api.call_args.args[2]['body'])

    @patch.object(module, 'github', return_value={'target_commitish': COMMIT, 'draft': False})
    def test_retry_does_not_create_duplicate(self, api):
        self.run_release()
        api.assert_called_once()

    @patch.object(module, 'github')
    def test_unverified_or_wrong_commit_receipt_blocks_release(self, api):
        for changes in ({'activeRevisionVerified': False}, {'sourceGitCommit': 'b' * 40}):
            self.receipt(**changes)
            with self.assertRaises(ValueError):
                self.run_release()
        api.assert_not_called()

    @patch.object(module.subprocess, 'run')
    def test_only_not_found_is_treated_as_missing_release(self, run):
        run.return_value = Mock(returncode=1, stderr='gh: Not Found (HTTP 404)')
        self.assertIsNone(module.github('synthetic/repo', 'releases/tags/test'))
        run.return_value = Mock(returncode=1, stderr='gh: Forbidden (HTTP 403)')
        with self.assertRaises(RuntimeError):
            module.github('synthetic/repo', 'releases/tags/test')


if __name__ == '__main__':
    unittest.main()
