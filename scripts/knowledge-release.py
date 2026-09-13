#!/usr/bin/env python3
"""Record a successfully verified publication; run only after the publisher succeeds."""
import json
import os
from pathlib import Path
import re
import subprocess


def github(repository, path, payload=None):
    command = ['gh', 'api', f'repos/{repository}/{path}']
    if payload is not None:
        command += ['--method', 'POST', '--input', '-']
    result = subprocess.run(command, input=json.dumps(payload) if payload else None,
                            capture_output=True, text=True)
    if result.returncode:
        if payload is None and '(HTTP 404)' in result.stderr:
            return None
        raise RuntimeError(result.stderr)
    return json.loads(result.stdout)


def release(directory, commit, selection, repository):
    if not re.fullmatch(r'[0-9a-f]{40}', commit):
        raise ValueError('A full source commit is required')
    if not re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9_-]*', selection):
        raise ValueError('Invalid office selection')
    receipts = sorted(p for p in directory.glob('*.json') if not p.name.endswith('-evaluation.json'))
    if not receipts:
        raise ValueError('No publication receipts')
    changed = []
    for path in receipts:
        receipt = json.loads(path.read_text())
        if (receipt.get('activeRevisionVerified') is not True
                or receipt.get('sourceGitCommit') != commit
                or not (receipt.get('applied') is True or receipt.get('unchanged') is True)):
            raise ValueError(f'Invalid publication receipt: {path.name}')
        # Provenance survives a retry that now reports unchanged. An older
        # revision checked by a newer Product release does not create a release.
        if receipt['provenance'] == f'git:{commit}':
            changed.append((receipt['officeKey'], receipt['revision']['id']))
    if not changed:
        print('Knowledge unchanged; no release needed.')
        return
    tag = f'knowledge-{commit}-{selection}'
    existing = github(repository, f'releases/tags/{tag}')
    if existing is not None:
        if existing['target_commitish'] != commit or existing.get('draft'):
            raise ValueError('Existing knowledge release does not match this publication')
        print(f'Knowledge release already exists: {tag}')
        return
    body = f'Office knowledge published and verified from `{commit}`.\n\n'
    body += '| Changed office | Published revision |\n| --- | --- |\n'
    body += ''.join(f'| {office} | `{revision}` |\n' for office, revision in sorted(changed))
    body += '\nVerification includes the active database revision and configured retrieval fixtures.\n'
    run_url = os.environ.get('GITHUB_SERVER_URL', 'https://github.com') + f'/{repository}/actions/runs/' + os.environ['GITHUB_RUN_ID']
    body += f'\n[Publication receipts and verification results]({run_url})\n'
    github(repository, 'releases', {
        'tag_name': tag, 'target_commitish': commit,
        'name': f'Knowledge {commit[:12]} ({selection})', 'body': body,
        'draft': False, 'prerelease': False, 'make_latest': 'false',
    })
    print(f'Created knowledge release: {tag}')


if __name__ == '__main__':
    release(Path(os.environ['KNOWLEDGE_OUTPUT_DIRECTORY']), os.environ['KNOWLEDGE_COMMIT'],
            os.environ.get('KNOWLEDGE_OFFICE', 'all'), os.environ['GITHUB_REPOSITORY'])
