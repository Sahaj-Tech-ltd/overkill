"""Installer network fixtures and strictly non-mutating release dry-run tests."""
import hashlib
import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


class InstallerReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='overkill-install-test-')
        self.addCleanup(self.temporary.cleanup)
        self.work = Path(self.temporary.name)
        self.bin = self.work / 'bin'
        self.bin.mkdir()
        self.assets = self.work / 'assets'
        self.assets.mkdir()
        self.env = dict(os.environ, PATH=f'{self.bin}:/usr/bin:/bin', HOME=str(self.work / 'home'),
                        OVERKILL_INSTALLER_SOURCE_ONLY='1', FIXTURE_ASSETS=str(self.assets))
        self.command('curl', '''
url=""; output=""
while [ "$#" -gt 0 ]; do
 case "$1" in -o) output="$2"; shift 2;; -*) shift;; *) url="$1"; shift;; esac
done
cp "$FIXTURE_ASSETS/${url##*/}" "$output"
''')
        self.command('uname', 'if [ "$1" = -s ]; then echo "$FIXTURE_OS"; else echo "$FIXTURE_ARCH"; fi\n')

    def command(self, name, body):
        path = self.bin / name
        path.write_text('#!/bin/sh\n' + body)
        path.chmod(0o755)

    def shell(self, command, **env):
        script = f'. "{ROOT / "install.sh"}"\n{command}'
        return subprocess.run(['/bin/sh', '-c', script], env=dict(self.env, **env),
                              text=True, capture_output=True, timeout=10, check=False)

    def test_all_posix_release_target_names(self):
        for system, machine, expected in [('Linux', 'x86_64', 'linux-amd64'),
                                          ('Linux', 'aarch64', 'linux-arm64'),
                                          ('Darwin', 'x86_64', 'darwin-amd64'),
                                          ('Darwin', 'arm64', 'darwin-arm64')]:
            result = self.shell('detect_platform; printf "%s-%s" "$PLATFORM" "$ARCH"',
                                FIXTURE_OS=system, FIXTURE_ARCH=machine)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, expected)

    def test_release_and_ci_matrices_cover_every_supported_target(self):
        targets = {('linux', 'amd64'), ('linux', 'arm64'), ('darwin', 'amd64'),
                   ('darwin', 'arm64'), ('windows', 'amd64')}
        release = (ROOT / '.github/workflows/release.yml').read_text()
        ci = (ROOT / '.github/workflows/ci.yml').read_text()
        pattern = r'- goos: (\w+)\s+goarch: (\w+)'
        self.assertEqual(set(re.findall(pattern, release)), targets)
        # Linux/amd64 is built by lint-build; all other published targets must
        # remain in the explicit required cross-build matrix.
        self.assertEqual(set(re.findall(pattern, ci)) | {('linux', 'amd64')}, targets)
        publication = release.split('  publish:', 1)[1]
        self.assertIn('actions/checkout@v4', publication)
        self.assertIn('GITHUB_REPOSITORY:', publication)
        self.assertNotIn('continue-on-error', publication)
        windows = ci.split('  windows-platform-tests:', 1)[1]
        self.assertIn('runs-on: windows-latest', windows)
        self.assertRegex(windows, r'(?m)^ {10}\./scripts/tests/test_installer_windows\.ps1\s*$')
        self.assertIn('OVERKILL_INSTALLER_TEST_BINARY:', windows)
        self.assertRegex(windows, r'(?m)^ {10}go build -trimpath -o .*OVERKILL_INSTALLER_TEST_BINARY.* ./cmd/overkill$')
        self.assertIn('shell: pwsh', windows)
        posix_fixtures = ci.split('  workflow-and-hooks:', 1)[1].split('  windows-platform-tests:', 1)[0]
        self.assertRegex(posix_fixtures, r'(?m)^ {10}python -m unittest discover -s scripts/tests -v$')
        self.assertRegex(posix_fixtures, r'(?m)^ {10}go build -trimpath -o .*OVERKILL_INSTALLER_TEST_BINARY.* ./cmd/overkill$')
        self.assertIn('OVERKILL_INSTALLER_TEST_BINARY:', posix_fixtures)

    def prepare_asset(self, name='overkill-linux-amd64'):
        content = b'#!/bin/sh\necho fixture-binary\n'
        (self.assets / name).write_bytes(content)
        return hashlib.sha256(content).hexdigest()

    def download(self):
        install = self.work / 'installed'
        install.mkdir(exist_ok=True)
        return self.shell(f'PLATFORM=linux; ARCH=amd64; INSTALL_DIR="{install}"; SUDO=""; step_download_binary')

    def test_verified_asset_installed(self):
        digest = self.prepare_asset()
        (self.assets / 'checksums.txt').write_text(f'{digest}  overkill-linux-amd64\n')
        result = self.download()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue((self.work / 'installed/overkill').exists())

    def test_every_posix_release_target_downloads_and_verifies(self):
        for system, machine, asset in [
                ('Linux', 'x86_64', 'overkill-linux-amd64'),
                ('Linux', 'aarch64', 'overkill-linux-arm64'),
                ('Darwin', 'x86_64', 'overkill-darwin-amd64'),
                ('Darwin', 'arm64', 'overkill-darwin-arm64')]:
            with self.subTest(asset=asset):
                digest = self.prepare_asset(asset)
                (self.assets / 'checksums.txt').write_text(f'{digest}  {asset}\n')
                destination = self.work / asset
                destination.mkdir()
                result = self.shell(f'detect_platform; INSTALL_DIR="{destination}"; SUDO=""; step_download_binary',
                                    FIXTURE_OS=system, FIXTURE_ARCH=machine)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual((destination / 'overkill').read_bytes(), (self.assets / asset).read_bytes())
                self.assertTrue(os.access(destination / 'overkill', os.X_OK))

    def test_installer_bootstrap_configuration_is_loaded_by_real_application(self):
        executable = os.environ.get('OVERKILL_INSTALLER_TEST_BINARY')
        if not executable:
            self.skipTest('Set OVERKILL_INSTALLER_TEST_BINARY to a freshly built native executable')
        executable = Path(executable).resolve(strict=True)
        home = self.work / 'home with spaces'
        environment = dict(OVERKILL_HOME=str(home), BIN_PATH=str(executable),
                           OPENAI_API_KEY='installer-secret-not-for-config',
                           ANTHROPIC_API_KEY='another-installer-secret-not-for-config')
        result = self.shell('step_bootstrap_home', **environment)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        config = home / 'config.toml'
        original = config.read_bytes()
        self.assertNotIn(b'secret-not-for-config', original)
        self.assertNotIn(b'[providers.openai]', original)
        result = subprocess.run([str(executable), '--config', str(config), 'config', 'validate'],
                                env=dict(self.env, **environment), text=True,
                                capture_output=True, timeout=10, check=False)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn('Errors:', result.stdout)
        result = self.shell('step_bootstrap_home', **environment)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(config.read_bytes(), original)
        # Confirm config.Load is the actual boundary: invalid legacy table
        # syntax must fail, rather than being silently replaced by defaults.
        config.write_text('[providers.openai]\napi_key = "test-only"\n')
        result = subprocess.run([str(executable), '--config', str(config), 'config', 'validate'],
                                env=dict(self.env, **environment), text=True,
                                capture_output=True, timeout=10, check=False)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_download_failure_preserves_existing_binary_and_configuration(self):
        destination = self.work / 'installed'
        destination.mkdir()
        target = destination / 'overkill'
        target.write_bytes(b'previous executable')
        config = self.work / 'existing-config.toml'
        config.write_bytes(b'operator-owned configuration')
        # The asset is deliberately absent; curl fails before verification or
        # executable replacement. This is an offline network-failure fixture.
        (self.assets / 'checksums.txt').write_text('0' * 64 + '  overkill-linux-amd64\n')
        result = self.download()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('failed to download', result.stderr)
        self.assertEqual(target.read_bytes(), b'previous executable')
        self.assertEqual(config.read_bytes(), b'operator-owned configuration')

    def test_checksum_case_is_normalized_and_malformed_hashes_are_rejected(self):
        digest = self.prepare_asset()
        manifest = self.assets / 'checksums.txt'
        manifest.write_text(digest.upper() + '  overkill-linux-amd64\n')
        result = self.download()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        for malformed in ['g' * 64, digest[:-1], digest + '0']:
            with self.subTest(hash=malformed):
                target = self.work / 'installed/overkill'
                target.write_bytes(b'previous executable')
                manifest.write_text(malformed + '  overkill-linux-amd64\n')
                result = self.download()
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(target.read_bytes(), b'previous executable')

    def test_complete_native_prebuilt_install_bootstraps_fresh_home_and_preserves_config(self):
        executable = os.environ.get('OVERKILL_INSTALLER_TEST_BINARY')
        if not executable:
            self.skipTest('Set OVERKILL_INSTALLER_TEST_BINARY to a freshly built native executable')
        import platform
        targets = {('Darwin', 'arm64'): ('Darwin', 'arm64', 'darwin-arm64'),
                   ('Darwin', 'x86_64'): ('Darwin', 'x86_64', 'darwin-amd64'),
                   ('Linux', 'x86_64'): ('Linux', 'x86_64', 'linux-amd64'),
                   ('Linux', 'aarch64'): ('Linux', 'aarch64', 'linux-arm64')}
        system, machine, target = targets[(platform.system(), platform.machine())]
        asset = 'overkill-' + target
        shutil.copyfile(Path(executable).resolve(strict=True), self.assets / asset)
        digest = hashlib.sha256((self.assets / asset).read_bytes()).hexdigest()
        (self.assets / 'checksums.txt').write_text(digest + '  ' + asset + '\n')
        home = self.work / 'Fresh Home With Spaces'
        home.mkdir()
        (home / '.zshrc').write_text('# existing shell settings\n')
        destination = home / 'Local Executables'
        environment = dict(HOME=str(home), OVERKILL_HOME=str(home / '.overkill'),
                           OVERKILL_INSTALL_DIR=str(destination), FIXTURE_OS=system,
                           FIXTURE_ARCH=machine)
        result = self.shell('main', **environment)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        installed = destination / 'overkill'
        self.assertEqual(installed.read_bytes(), (self.assets / asset).read_bytes())
        cfg = home / '.overkill/config.toml'
        original = cfg.read_bytes()
        for command in [('--version',), ('--help',), ('--config', str(cfg), 'config', 'validate')]:
            result = subprocess.run([str(installed), *command], cwd=self.work,
                                    env=dict(self.env, **environment), text=True,
                                    capture_output=True, timeout=10, check=False)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        result = self.shell('main', **environment)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(cfg.read_bytes(), original)
        self.assertEqual((home / '.zshrc').read_text().count('# added by overkill installer'), 1)
        for sub in ['sessions', 'plugins', 'cache', 'journal/raw', 'memories']:
            self.assertTrue((home / '.overkill' / sub).is_dir())

    def test_manifest_failures_preserve_installed_binary(self):
        digest = self.prepare_asset()
        cases = [None, '', '0' * 64 + '  overkill-linux-amd64\n',
                 f'{digest}  another-asset\n',
                 f'{digest}  overkill-linux-amd64\n{digest}  overkill-linux-amd64\n']
        for manifest in cases:
            with self.subTest(manifest=manifest):
                target = self.work / 'installed/overkill'
                target.parent.mkdir(exist_ok=True)
                target.write_text('preserve old binary')
                path = self.assets / 'checksums.txt'
                path.unlink(missing_ok=True)
                if manifest is not None:
                    path.write_text(manifest)
                result = self.download()
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(target.read_text(), 'preserve old binary')

    def test_release_dry_run_preserves_all_existing_artifacts(self):
        repo = self.work / 'repo'
        (repo / 'scripts').mkdir(parents=True)
        shutil.copy(ROOT / 'scripts/release.sh', repo / 'scripts/release.sh')
        (repo / 'dist').mkdir()
        sentinel = repo / 'dist/checksums.txt'
        sentinel.write_bytes(b'existing checksum manifest\x00')
        prior = {str(p.relative_to(repo)): p.read_bytes() for p in repo.rglob('*') if p.is_file()}
        result = subprocess.run(['/bin/bash', str(repo / 'scripts/release.sh'), 'v1.2.3', '--dry-run'],
                                capture_output=True, text=True, timeout=5, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        after = {str(p.relative_to(repo)): p.read_bytes() for p in repo.rglob('*') if p.is_file()}
        self.assertEqual(after, prior)

    def prepare_release(self, failed_target=''):
        repo = self.work / 'preparation-repo'
        (repo / 'scripts').mkdir(parents=True)
        for name in ('release.sh', 'verify-release-assets.py'):
            shutil.copy(ROOT / 'scripts' / name, repo / 'scripts' / name)
        # Exercise the actual preparation shell transaction while substituting
        # compiler and Git effects. These fixtures never create a real tag.
        self.command('go', '''
[ "${FIXTURE_FAIL_TARGET:-}" != "$GOOS/$GOARCH" ] || exit 1
while [ "$#" -gt 0 ]; do
 if [ "$1" = -o ]; then printf '%s/%s fixture bytes' "$GOOS" "$GOARCH" > "$2"; exit 0; fi
 shift
done
exit 1
''')
        self.command('git', '''
printf '%s\\n' "$*" >> "$GIT_FIXTURE_LOG"
case "$1" in status) exit 0;; rev-parse) exit 1;; tag) exit 0;; *) exit 1;; esac
''')
        log = self.work / 'git-effects'
        result = subprocess.run(['/bin/bash', str(repo / 'scripts/release.sh'), 'v1.2.3'],
                                env=dict(self.env, GIT_FIXTURE_LOG=str(log), FIXTURE_FAIL_TARGET=failed_target),
                                text=True, capture_output=True, timeout=10, check=False)
        return result, repo, log

    def test_release_preparation_verifies_complete_assets_before_local_tag(self):
        result, repo, log = self.prepare_release()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('Verified 5 release assets', result.stdout)
        self.assertEqual(len((repo / 'dist/v1.2.3/checksums.txt').read_text().splitlines()), 5)
        self.assertIn('tag -a v1.2.3 -m Release v1.2.3', log.read_text())
        self.assertNotIn('push', log.read_text())

    def test_release_preparation_failed_build_cannot_tag_or_stage_partial_release(self):
        result, repo, log = self.prepare_release(failed_target='windows/amd64')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((repo / 'dist').exists())
        self.assertNotIn('tag ', log.read_text())

    def publication(self, mismatch=False, mutation=None, version='v1.2.3-beta.1'):
        import textwrap
        dist = self.work / 'dist'
        dist.mkdir()
        names = ['overkill-linux-amd64', 'overkill-linux-arm64',
                 'overkill-darwin-amd64', 'overkill-darwin-arm64', 'overkill-windows-amd64.exe']
        entries = []
        for name in names:
            content = name.encode()
            (dist / name).write_bytes(content)
            entries.append(f'{hashlib.sha256(content).hexdigest()}  {name}\n')
        (dist / 'checksums.txt').write_text(''.join(entries))
        if mismatch:
            (dist / names[0]).write_text('modified after checksum generation')
        if mutation:
            mutation(dist, names)
        (self.work / 'scripts').mkdir()
        shutil.copy(ROOT / 'scripts/verify-release-assets.py', self.work / 'scripts/verify-release-assets.py')
        log = self.work / 'gh-arguments'
        self.command('gh', 'printf "%s\\n" "$@" > "$GH_FIXTURE_LOG"\n')
        workflow = (ROOT / '.github/workflows/release.yml').read_text()
        publication = textwrap.dedent(workflow.rsplit('run: |', 1)[1])
        result = subprocess.run(['/bin/bash', '-e', '-c', publication], cwd=self.work,
                                env=dict(self.env, GITHUB_REF='refs/tags/' + version,
                                         GITHUB_REPOSITORY='fixture-owner/fixture-repo', GH_FIXTURE_LOG=str(log)),
                                text=True, capture_output=True, timeout=10, check=False)
        return result, log

    def test_publication_inputs_use_explicit_repository_generated_notes_and_prerelease(self):
        result, log = self.publication()
        self.assertEqual(result.returncode, 0, result.stderr)
        arguments = log.read_text().splitlines()
        self.assertIn('--repo', arguments)
        self.assertIn('fixture-owner/fixture-repo', arguments)
        self.assertIn('--generate-notes', arguments)
        self.assertIn('--prerelease', arguments)
        self.assertNotIn('--notes-file', arguments)
        self.assertIn('dist/overkill-windows-amd64.exe', arguments)
        assets = [arg for arg in arguments if arg.startswith('dist/')]
        self.assertEqual(set(assets), {'dist/' + name for name in [
            'overkill-linux-amd64', 'overkill-linux-arm64', 'overkill-darwin-amd64',
            'overkill-darwin-arm64', 'overkill-windows-amd64.exe', 'checksums.txt']})

    def test_stable_publication_is_not_prerelease(self):
        result, log = self.publication(version='v1.2.3')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('--prerelease', log.read_text().splitlines())

    def test_publication_rejects_asset_missing_from_manifest(self):
        def omit(dist, names):
            manifest = dist / 'checksums.txt'
            manifest.write_text('\n'.join(manifest.read_text().splitlines()[1:]) + '\n')
        result, log = self.publication(mutation=omit)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(log.exists())

    def test_publication_rejects_duplicate_manifest_entry(self):
        def duplicate(dist, names):
            manifest = dist / 'checksums.txt'
            lines = manifest.read_text().splitlines()
            manifest.write_text('\n'.join(lines + [lines[0]]) + '\n')
        result, log = self.publication(mutation=duplicate)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(log.exists())

    def test_publication_requires_every_release_target(self):
        result, log = self.publication(mutation=lambda dist, names: (dist / names[-1]).unlink())
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(log.exists())

    def test_publication_rejects_unverified_extra_asset(self):
        result, log = self.publication(mutation=lambda dist, names: (dist / 'overkill-extra').write_text('extra'))
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(log.exists())

    def test_publication_rejects_symlink_asset(self):
        def symlink(dist, names):
            (dist / names[0]).unlink()
            (dist / names[0]).symlink_to(names[1])
        result, log = self.publication(mutation=symlink)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(log.exists())

    def test_publication_rejects_changed_artifact_before_calling_github(self):
        result, log = self.publication(mismatch=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(log.exists())


if __name__ == '__main__':
    unittest.main()
