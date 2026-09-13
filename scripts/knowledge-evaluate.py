#!/usr/bin/env python3
"""Run synthetic retrieval acceptance cases against the deployed agent API."""
import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
import urllib.parse


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cases', required=True)
    parser.add_argument('--office')
    parser.add_argument('--url')
    parser.add_argument('--validate-only', action='store_true')
    parser.add_argument('--revision', help='Require the just-published revision')
    args = parser.parse_args()
    with open(args.cases) as source:
        cases = json.load(source)
    if not isinstance(cases, list) or not cases:
        parser.error('cases must be a nonempty list')
    ids = set()
    for case in cases:
        if (not isinstance(case, dict) or
                not all(isinstance(case.get(k), str) and case[k].strip() for k in ('id', 'query')) or
                case['id'] in ids or len(case['query']) > 500 or
                case.get('expectedOutcome') not in ('found', 'no_relevant_information') or
                not isinstance(case.get('expectedSectionIds'), list) or
                not isinstance(case.get('excludedSectionIds', []), list) or
                not isinstance(case.get('allowedSectionIds', []), list) or
                not all(isinstance(v, str) for v in case['expectedSectionIds'] + case.get('excludedSectionIds', []) + case.get('allowedSectionIds', [])) or
                not isinstance(case.get('maxResponseCharacters'), int) or case['maxResponseCharacters'] <= 0):
            parser.error('invalid retrieval acceptance case')
        ids.add(case['id'])
    if args.validate_only:
        print(json.dumps({'validatedCases': len(cases)}))
        return 0
    endpoint = urllib.parse.urlsplit(args.url or '')
    if endpoint.scheme != 'https' or not endpoint.hostname or endpoint.username or endpoint.password or not args.office:
        parser.error('--office and an HTTPS --url without credentials are required')
    token = os.environ.get('KNOWLEDGE_SERVICE_TOKEN')
    if not token:
        parser.error('KNOWLEDGE_SERVICE_TOKEN is required')
    opener = urllib.request.build_opener(NoRedirect)
    results = []
    for case in cases:
        request = urllib.request.Request(
            args.url.rstrip('/') + '/v1/agent/knowledge/search',
            data=json.dumps({'query': case['query']}).encode(),
            headers={'Authorization': 'Bearer ' + token,
                     'X-Office-Key': args.office, 'Content-Type': 'application/json'})
        started = time.monotonic()
        errors = []
        try:
            with opener.open(request, timeout=4) as response:
                result = json.load(response)
            passages = result.get('passages', [])
            ids = {p['sectionId'] for p in passages}
            if result.get('outcome') != case['expectedOutcome']:
                errors.append('unexpected outcome')
            missing = set(case['expectedSectionIds']) - ids
            excluded = set(case.get('excludedSectionIds', [])) & ids
            if missing:
                errors.append('missing entries: ' + ', '.join(sorted(missing)))
            if excluded:
                errors.append('unrelated entries: ' + ', '.join(sorted(excluded)))
            if 'allowedSectionIds' in case and ids - set(case['allowedSectionIds']):
                errors.append('extra entries: ' + ', '.join(sorted(ids - set(case['allowedSectionIds']))))
            characters = sum(len(p['text']) + len(p['title']) for p in passages)
            if characters > case['maxResponseCharacters']:
                errors.append('response exceeds evidence budget')
            if args.revision and (result.get('revisionId') != args.revision or
                                  any(p.get('revisionId') != args.revision for p in passages)):
                errors.append('unexpected active revision')
        except (urllib.error.URLError, TimeoutError, ValueError, KeyError) as exc:
            errors.append('request failed: ' + type(exc).__name__)
            ids, characters = set(), 0
        results.append({'case': case['id'], 'passed': not errors, 'errors': errors,
                        'sectionIds': sorted(ids), 'characters': characters,
                        'elapsedMs': round((time.monotonic() - started) * 1000)})
    print(json.dumps({'passed': all(r['passed'] for r in results), 'cases': results}, indent=2))
    return 0 if all(r['passed'] for r in results) else 1


if __name__ == '__main__':
    sys.exit(main())
