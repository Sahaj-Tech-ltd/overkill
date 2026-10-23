"""Hook command fixtures: no network, tools, staged state, or real Git mutations."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[2]


class HookTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'scripts').mkdir()
        (self.root / 'bridge').mkdir()
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        for name in ('hook-common.sh', 'pre-commit.sh', 'pre-push.sh'):
            shutil.copy(REPO / 'scripts' / name, self.root / 'scripts' / name)
        self.env = dict(os.environ, PATH=f'{self.bin}:/usr/bin:/bin', FIXTURE_ROOT=str(self.root))
        self.command('git', '''
case "$1" in
rev-parse) printf '%s\\n' "$FIXTURE_ROOT" ;;
diff) printf '%s\\n' 'example.go' 'bridge/test.py' 'tui/src/example.ts' ;;
ls-files) printf '%s\\n' 'example.go' ;;
esac
''')
        self.command('gofmt', 'exit 0\n')
        for command in ('go', 'ruff', 'python', 'npm', 'gitleaks'):
            self.command(command, '''
printf '%s %s\\n' "$(basename "$0")" "$*" >> "$FIXTURE_ROOT/commands"
exit "${FIXTURE_FAILURE:-0}"
''')

    def command(self, name, body):
        path = self.bin / name
        path.write_text('#!/bin/bash\n' + body)
        path.chmod(0o755)

    def run_hook(self, name, failure=0):
        return subprocess.run(
            ['/bin/bash', str(self.root / 'scripts' / name)],
            env=dict(self.env, FIXTURE_FAILURE=str(failure)), capture_output=True, text=True,
            timeout=10,
        )

    def test_precommit_success_and_bounded_python(self):
        result = self.run_hook('pre-commit.sh')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        commands = (self.root / 'commands').read_text()
        self.assertIn('python -m pytest -x -q --timeout=30', commands)
        self.assertIn('npm --prefix tui run test:build', commands)

    def test_precommit_aggregates_silent_failures(self):
        result = self.run_hook('pre-commit.sh', 1)
        self.assertEqual(result.returncode, 1)
        self.assertIn('7 check(s) failed.', result.stdout)
        self.assertIn('TUI runtime imports ... FAIL', result.stdout)

    def test_prepush_success_without_gnu_timeout(self):
        result = self.run_hook('pre-push.sh')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        commands = (self.root / 'commands').read_text()
        self.assertIn('gitleaks detect --timeout 30', commands)
        self.assertIn('go test -race', commands)

    def test_prepush_aggregates_silent_failures(self):
        result = self.run_hook('pre-push.sh', 2)
        self.assertEqual(result.returncode, 1)
        self.assertIn('9 check(s) failed.', result.stdout)
        self.assertIn('npm audit ... FAIL', result.stdout)

    def test_missing_tool_is_failed_check(self):
        (self.bin / 'gitleaks').unlink()
        result = self.run_hook('pre-push.sh')
        self.assertEqual(result.returncode, 1)
        self.assertIn('Secret scan ... FAIL (exit 127)', result.stdout)
        self.assertIn('1 check(s) failed.', result.stdout)


if __name__ == '__main__':
    unittest.main()
