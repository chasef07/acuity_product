#!/usr/bin/env python3
"""Exercise publisher orchestration with real Git and isolated fake services."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

PUBLISHER = Path(__file__).with_name('knowledge-publish.sh').resolve()


class PublishTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'knowledge/offices').mkdir(parents=True)
        (self.root / 'scripts').mkdir()
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        self.log = self.root / 'events'
        self.env = dict(os.environ, PATH=f'{self.bin}:{os.environ["PATH"]}',
                        EVENTS=str(self.log), KNOWLEDGE_GOOGLE_PROJECT='synthetic-project',
                        KNOWLEDGE_SQL_INSTANCE='synthetic-instance',
                        KNOWLEDGE_DATABASE_SECRET='synthetic-database',
                        KNOWLEDGE_OPERATOR_EMAIL='operator@example.com',
                        KNOWLEDGE_OUTPUT_DIRECTORY=str(self.root / 'receipts'),
                        GITHUB_STEP_SUMMARY=str(self.root / 'summary'))
        for key in ('KNOWLEDGE_OFFICE', 'FAIL_VALIDATE', 'FAIL_APPLY', 'UNCHANGED', 'FAIL_EVAL'):
            self.env.pop(key, None)
        self.executable('cli', '''import json,os,sys
from pathlib import Path
args=sys.argv[1:]; office=Path(args[args.index('--source')+1]).stem
phase='apply' if '--apply' in args else 'validate'
with open(os.environ['EVENTS'],'a') as f: f.write(f'{phase}:{office}\\n')
if os.environ.get('FAIL_'+phase.upper())==office: sys.exit(7)
print(json.dumps({'unchanged':os.environ.get('UNCHANGED')==office,'revision':{'id':'synthetic-'+office}}))
''')
        self.executable('go', '''import os,shutil,sys
shutil.copy(os.path.join(os.path.dirname(sys.argv[0]),'cli'),sys.argv[sys.argv.index('-o')+1])
''')
        self.executable('gcloud', '''import os
with open(os.environ['EVENTS'],'a') as f: f.write('secret\\n')
print('synthetic-value')
''')
        self.executable('cloud-sql-proxy', 'import time\ntime.sleep(120)\n')
        # Only stub the publisher's inline socket-readiness check. Run JSON
        # receipt parsing and the fake evaluator with the real Python runtime.
        self.executable('python3', f'''import os,sys
if sys.argv[1:]==['-']: sys.exit(0)
os.execv({sys.executable!r},[{sys.executable!r}]+sys.argv[1:])
''')
        for office in ('alpha', 'beta', 'gamma'):
            (self.root / f'knowledge/offices/{office}.yaml').write_text('synthetic source\n')
        self.git('init', '-q')
        self.git('config', 'user.name', 'Synthetic Test')
        self.git('config', 'user.email', 'test@example.com')
        self.commit()

    def executable(self, name, body):
        path = self.bin / name
        path.write_text(f'#!{sys.executable}\n' + body)
        path.chmod(0o755)

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, text=True).strip()

    def commit(self):
        self.git('add', 'knowledge', 'scripts')
        self.git('commit', '-qm', 'Synthetic fixtures')
        self.env['GITHUB_SHA'] = self.git('rev-parse', 'HEAD')

    def run_publish(self, **env):
        result = subprocess.run(['bash', str(PUBLISHER)], cwd=self.root,
                                env=dict(self.env, **env), capture_output=True, text=True, timeout=15)
        events = self.log.read_text().splitlines() if self.log.exists() else []
        return result, events

    def test_default_all_validates_before_writes_and_receipts(self):
        result, events = self.run_publish(UNCHANGED='beta')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(events[:3], ['validate:alpha', 'validate:beta', 'validate:gamma'])
        self.assertEqual([x for x in events if x.startswith('apply:')],
                         ['apply:alpha', 'apply:beta', 'apply:gamma'])
        for office in ('alpha', 'beta', 'gamma'):
            receipt = json.loads((self.root / f'receipts/{office}.json').read_text())
            self.assertEqual(receipt['revision']['id'], 'synthetic-' + office)
        self.assertIn('| beta | unchanged |', (self.root / 'summary').read_text())
        self.assertIn('| gamma | published |', (self.root / 'summary').read_text())

    def test_explicit_one_office(self):
        result, events = self.run_publish(KNOWLEDGE_OFFICE='beta')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([x for x in events if ':' in x], ['validate:beta', 'apply:beta'])
        self.assertEqual([x.name for x in (self.root / 'receipts').glob('*.json')], ['beta.json'])

    def test_invalid_source_prevents_every_write(self):
        result, events = self.run_publish(FAIL_VALIDATE='beta')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(events, ['validate:alpha', 'validate:beta'])

    def test_apply_failure_continues_and_fails_aggregate(self):
        result, events = self.run_publish(FAIL_APPLY='beta')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('apply:gamma', events)
        summary = (self.root / 'summary').read_text()
        self.assertIn('| beta | failed |', summary)
        self.assertIn('| gamma | published |', summary)

    def test_evaluation_failure_continues_and_fails_aggregate(self):
        (self.root / 'knowledge/evals').mkdir()
        (self.root / 'knowledge/evals/alpha.json').write_text('[]')
        (self.root / 'scripts/knowledge-evaluate.py').write_text('''import os,sys
phase='eval-validate' if '--validate-only' in sys.argv else 'eval'
with open(os.environ['EVENTS'],'a') as f: f.write(phase+'\\n')
sys.exit(0 if phase=='eval-validate' else 9)
''')
        self.commit()
        result, events = self.run_publish(KNOWLEDGE_API_URL='https://synthetic.invalid',
                                          KNOWLEDGE_SERVICE_TOKEN_SECRET='synthetic-token')
        self.assertNotEqual(result.returncode, 0)
        self.assertLess(events.index('eval-validate'), events.index('apply:alpha'))
        self.assertIn('apply:gamma', events)
        self.assertIn('retrieval verification failed', (self.root / 'summary').read_text())
        self.assertTrue((self.root / 'receipts/alpha-evaluation.json').exists())


if __name__ == '__main__':
    unittest.main()
