#!/usr/bin/env python3
"""Extract source-bound DDL from the audit's checked-out upstream baseline.

No historical Go code or dependencies are run. The fixture captures the ordered
SQL statements (and the known historical cost-index failure), not successful
execution of a historical application binary or an archived release upgrade.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess

COMMIT = '34b3fab9d33bff34e0b00ae7e120498e0e6a79ee'
SOURCES = [
    'internal/db/db.go', 'internal/session/postgres.go',
    'internal/agent/goal.go', 'internal/cost/postgres.go',
    'internal/learning/store.go', 'internal/cron/store.go',
    'internal/daemon/jobs.go', 'internal/memory/postgres.go',
]
ROOT = Path(__file__).resolve().parent.parent
OUTPUT = ROOT / 'internal/dbschema/testdata/baseline-34b3fab.json'

def build():
    sources = []
    for path in SOURCES:
        raw = subprocess.check_output(['git', 'show', f'{COMMIT}:{path}'], cwd=ROOT)
        source = raw.decode()
        statements = []
        for match in re.finditer(r'`([^`]*)`', source):
            sql = match.group(1).strip()
            if not re.match(r'^(CREATE|ALTER)\s', sql):
                continue
            statements.append({
                'start_line': source.count('\n', 0, match.start()) + 1,
                'end_line': source.count('\n', 0, match.end()) + 1,
                'sha256': hashlib.sha256(sql.encode()).hexdigest(),
                'expected_sqlstate': '42P17' if 'idx_cost_records_date' in sql else '',
                'sql': sql,
            })
        sources.append({
            'path': path,
            'git_blob': subprocess.check_output(['git', 'rev-parse', f'{COMMIT}:{path}'], cwd=ROOT).decode().strip(),
            'source_sha256': hashlib.sha256(raw).hexdigest(),
            'statements': statements,
        })
    return json.dumps({
        'version': 1, 'commit': COMMIT,
        'scope': 'Exact ordered DDL extracted from baseline source; historical Go code is not executed. The known non-immutable timestamp date index is expected to fail, preserving prior DDL as that constructor did.',
        'sources': sources,
    }, indent=2) + '\n'

if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    expected = build()
    if args.check:
        if OUTPUT.read_text() != expected:
            raise SystemExit('historical schema fixture differs from source-bound extraction')
        print('Historical fixture matches all eight baseline source files and DDL spans.')
    else:
        OUTPUT.write_text(expected)
        print(OUTPUT)
