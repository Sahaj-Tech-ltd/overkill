#!/usr/bin/env node
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {fileURLToPath} from 'node:url';
import {execFile, spawn} from 'node:child_process';
import {promisify} from 'node:util';
import {createHash} from 'node:crypto';
const exec = promisify(execFile);
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
async function main() {
  if (process.platform !== 'darwin' || process.arch !== 'arm64' || ![3, 4].includes(process.argv.length) || (process.argv[3] && !['--install-only', '--mock-storage-smoke'].includes(process.argv[3]))) throw new Error('Usage: node scripts/test-installed-desktop.mjs /absolute/path/to/development.dmg [--install-only|--mock-storage-smoke]');
  const mockStorageSmoke = process.argv[3] === '--mock-storage-smoke';
  const dmg = path.resolve(process.argv[2]); await fs.access(dmg);
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-installed-'));
  const mount = path.join(directory, 'Mounted Development Image'); await fs.mkdir(mount);
  const specifiedInstall = process.env.OVERKILL_TEST_INSTALL_DIRECTORY;
  if (specifiedInstall && !path.isAbsolute(specifiedInstall)) throw new Error('The test installation directory must be absolute.');
  const install = specifiedInstall || path.join(directory, 'Installed Apps With Spaces');
  await fs.mkdir(install, {recursive: true, mode: 0o700});
  if (specifiedInstall) {
    const marker = path.join(install, '.overkill-disposable-install');
    let owner;
    try { owner = await fs.readFile(marker, 'utf8'); }
    catch (error) {
      if (error.code !== 'ENOENT') throw error;
      if ((await fs.readdir(install)).length) throw new Error('Refusing an unowned nonempty test installation directory.');
      await fs.writeFile(marker, 'overkill-disposable-install-v1\n', {flag: 'wx', mode: 0o600});
      owner = 'overkill-disposable-install-v1\n';
    }
    if (owner !== 'overkill-disposable-install-v1\n') throw new Error('Invalid disposable installation ownership marker.');
  }
  let attached = false;
  let passed = false;
  try {
    await exec('/usr/bin/hdiutil', ['verify', dmg]);
    await exec('/usr/bin/hdiutil', ['attach', '-readonly', '-nobrowse', '-mountpoint', mount, dmg]); attached = true;
    const source = path.join(mount, 'Overkill Development.app');
    const installed = path.join(install, 'Overkill Development.app');
    let reuseInstalled = false;
    if (specifiedInstall && process.argv[3] !== '--install-only') {
      try {
        await fs.access(installed);
        await exec('/usr/bin/codesign', ['--verify', '--deep', '--strict', installed]);
        const signature = async app => {
          const {stderr} = await exec('/usr/bin/codesign', ['--display', '--verbose=4', app]);
          const hash = stderr.match(/^CDHash=([a-f0-9]+)$/m)?.[1];
          if (!hash) throw new Error('Missing installed application signature hash.');
          return hash;
        };
        if (await signature(installed) !== await signature(source)) throw new Error('Fixed installation differs from the image; prepare it with --install-only first.');
        reuseInstalled = true;
      } catch (error) {
        if (error.code !== 'ENOENT') throw error;
      }
    }
    if (!reuseInstalled) {
      if (specifiedInstall) await fs.rm(installed, {recursive: true, force: true});
      await exec('/usr/bin/ditto', [source, installed]);
    }
    await exec('/usr/bin/codesign', ['--verify', '--deep', '--strict', installed]);
    console.log(`Installed test app: ${installed}`);
    if (process.argv[3] === '--install-only') return;
    // The test's real fixture provider, backend and private database operate
    // inside the installed app. No source resource override is accepted.
    const env = {...process.env, OVERKILL_REQUIRE_DESKTOP_TEST: '1', OVERKILL_INSTALLED_MOCK_STORAGE_SMOKE: mockStorageSmoke ? '1' : '', OVERKILL_INSTALLED_DESKTOP_EXECUTABLE: path.join(installed, 'Contents/MacOS/Overkill'), OVERKILL_TEST_REINSTALL_SOURCE: source};
    await new Promise((resolve, reject) => {
      const child = spawn(process.execPath, ['--test', path.join(root, 'desktop/test/native.test.cjs')], {env, cwd: directory, stdio: 'inherit'});
      child.once('error', reject);
      child.once('exit', (code, signal) => code === 0 ? resolve() : reject(new Error(`Installed acceptance exited ${code ?? signal}`)));
    });
    await exec('/usr/bin/codesign', ['--verify', '--deep', '--strict', installed]);
    passed = true;
  } finally {
    if (attached) await exec('/usr/bin/hdiutil', ['detach', mount]);
    await fs.rm(directory, {recursive: true, force: true});
  }
  if (passed) {
    const {stdout: macOS} = await exec('/usr/bin/sw_vers', ['-productVersion']);
    const report = {schema: 1, completedAt: new Date().toISOString(), dmg: path.basename(dmg), dmgSHA256: createHash('sha256').update(await fs.readFile(dmg)).digest('hex'), host: {macOS: macOS.trim(), architecture: process.arch}, passed: true, cases: ['read-only image mount and integrity', 'copied app signature', 'installation path with spaces', 'isolated fresh profile and encrypted setup', 'bundled backend and database', 'actual workspace file edit', 'provider stream cancellation', 'frontend/backend peer exchange', 'same-version app replacement', 'persisted chat and correspondence after relaunch', 'owned database crash and recovery'], limitations: ['controlled local provider; no paid provider', 'same-version replacement; no cross-version migration claim', 'current host only; minimum macOS 13 not executed', 'local ad-hoc app; no Gatekeeper/notarization acceptance'], distributionReady: false};
    const reportName = mockStorageSmoke ? 'installed-mock-storage-smoke.json' : 'installed-acceptance.json';
    if (mockStorageSmoke) {
      report.mode = 'installed-mock-storage-smoke';
      report.passed = true; // Only the bounded smoke cases listed here.
      report.realInstalledKeychainAcceptance = 'pending';
      report.credentialStorage = 'fake-only safeStorage simulation in the disposable test process; no encryption claim';
      report.cases = report.cases.map(value => value === 'isolated fresh profile and encrypted setup' ? 'isolated fresh profile with fake-only mock storage' : value === 'persisted chat and correspondence after relaunch' ? 'persisted chat and correspondence after relaunch with explicit settings reconnect' : value);
      report.limitations.push('real installed safeStorage encryption, Keychain authorization and automatic credential startup are not tested');
    }
    await fs.writeFile(path.join(path.dirname(dmg), reportName), JSON.stringify(report, null, 2) + '\n');
    const artifactPath = path.join(path.dirname(dmg), 'artifact.json');
    let artifact;
    try { artifact = JSON.parse(await fs.readFile(artifactPath, 'utf8')); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
    if (artifact) {
      if (artifact.dmgSHA256 !== report.dmgSHA256) throw new Error('Artifact metadata does not match the tested image.');
      if (mockStorageSmoke) {
        artifact.installedMockStorageSmoke = `passed: ${reportName} (fake-only storage; real installed Keychain acceptance pending)`;
        artifact.realInstalledKeychainAcceptance = 'pending';
      } else artifact.installedAcceptance = 'passed: installed-acceptance.json (bounded local development acceptance)';
      await fs.writeFile(artifactPath, JSON.stringify(artifact, null, 2) + '\n');
    }
    if (mockStorageSmoke) console.log('PASS: bounded installed-app smoke with fake-only storage; real installed Keychain acceptance remains pending.');
    else console.log('PASS: mounted DMG, copied app, isolated first launch, tool edit, peer exchange, same-version reinstall, relaunch and database recovery.');
  }
}
main().catch(error => { console.error(error.message); process.exitCode = 1; });
