#!/usr/bin/env node
import fs from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {syncDesktopCanvas} from './desktop-canvas-assets.mjs';
import {syncDesktopTerminal} from './desktop-terminal-assets.mjs';
import {collectGoNotices} from './windows-go-notices.mjs';
const exec = promisify(execFile);
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const run = async (command, args, options = {}) => exec(command, args, {cwd: root, windowsHide: true, maxBuffer: 8 * 1024 * 1024, ...options});
export const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
export function safeArchiveEntries(text) {
  const entries = text.split(/\r?\n/).filter(Boolean);
  if (!entries.length || entries.length > 30000) throw new Error('Archive entry count is invalid.');
  for (const entry of entries) {
    if (entry.includes('\\') || /^[A-Za-z]:/.test(entry) || entry.startsWith('/') || entry.split('/').some(part => part === '..' || part.includes(':')) || /[\x00-\x1f]/.test(entry)) throw new Error('Unsafe archive path.');
  }
  return entries;
}
async function hashFile(file) { return sha256(await fs.readFile(file)); }
async function inventory(directory, prefix = '') {
  const records = [];
  for (const entry of (await fs.readdir(directory, {withFileTypes: true})).sort((a, b) => a.name.localeCompare(b.name))) {
    const file = path.join(directory, entry.name), relative = prefix + entry.name;
    if (entry.isDirectory()) records.push(...await inventory(file, relative + '/'));
    else if (entry.isFile()) records.push({path: relative, bytes: (await fs.stat(file)).size, sha256: await hashFile(file)});
    else throw new Error(`Non-regular packaged input: ${relative}`);
  }
  return records;
}
async function download(pin, file) {
  let bytes;
  try { bytes = await fs.readFile(file); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  if (!bytes || sha256(bytes) !== pin.sha256) {
    const response = await fetch(pin.url, {signal: AbortSignal.timeout(180000)});
    if (!response.ok) throw new Error(`Pinned download failed: ${response.status}`);
    const chunks = []; let length = 0;
    for await (const chunk of response.body) {
      length += chunk.length;
      if (length > 128 * 1024 * 1024) throw new Error('Pinned download exceeded size bound.');
      chunks.push(chunk);
    }
    bytes = Buffer.concat(chunks);
    if (sha256(bytes) !== pin.sha256) throw new Error('Pinned download checksum mismatch.');
    await fs.writeFile(file, bytes);
  }
  return file;
}
async function extract(archive, destination) {
  const {stdout} = await run('tar.exe', ['-tf', archive]);
  safeArchiveEntries(stdout);
  await fs.mkdir(destination, {recursive: true});
  await run('tar.exe', ['-xf', archive, '-C', destination]);
}
export async function main() {
  if (process.platform !== 'win32' || process.arch !== 'x64' || process.argv.length !== 2) throw new Error('Run without arguments on native Windows x64.');
  const pins = JSON.parse(await fs.readFile(path.join(root, 'scripts/windows-desktop-pins.json'), 'utf8'));
  const metadata = JSON.parse(await fs.readFile(path.join(root, 'desktop/package.json'), 'utf8'));
  if (metadata.devDependencies.electron !== pins.electron) throw new Error('Electron metadata differs from pinned Windows runtime.');
  if (!/^\d+\.\d+\.\d+(?:-[a-zA-Z0-9.-]+)?$/.test(metadata.version)) throw new Error('Invalid desktop package version.');
  const sourceCommit = (await run('git', ['rev-parse', 'HEAD'])).stdout.trim();
  if (process.env.GITHUB_SHA && process.env.GITHUB_SHA !== sourceCommit) throw new Error('Build checkout differs from requested workflow commit.');
  await run('git', ['diff', '--exit-code', 'HEAD']);
  await syncDesktopCanvas(root, {check: true});
 await syncDesktopTerminal(root,{check:true});
  const electron = path.join(root, 'desktop/node_modules/electron/dist');
  if ((await fs.readFile(path.join(electron, 'version'), 'utf8')).trim() !== pins.electron) throw new Error('Electron installed runtime is not the pinned version.');
  const output = path.join(root, '.cache/windows-desktop');
  await fs.mkdir(output, {recursive: true});
  const cache = path.join(output, 'downloads'); await fs.mkdir(cache, {recursive: true});
  const stage = path.join(output, 'app');
  await fs.rm(stage, {recursive: true, force: true}); await fs.cp(electron, stage, {recursive: true});
  await fs.rename(path.join(stage, 'electron.exe'), path.join(stage, 'Overkill.exe'));
  const resources = path.join(stage, 'resources');
  await fs.rm(path.join(resources, 'default_app.asar'), {force: true});
  const application = path.join(resources, 'app'); await fs.mkdir(application, {recursive: true});
  await fs.cp(path.join(root, 'desktop/src'), path.join(application, 'src'), {recursive: true});
  await fs.writeFile(path.join(application, 'package.json'), JSON.stringify({name: metadata.name, productName: 'Overkill', version: metadata.version, main: metadata.main, description: metadata.description}));
  await fs.copyFile(path.join(root, 'LICENSE-MIT'), path.join(application, 'LICENSE'));
  await fs.copyFile(path.join(root, 'LICENSE-APACHE'), path.join(application, 'LICENSE-APACHE'));
  const postgresJar = await download(pins.postgres, path.join(cache, 'postgres-17.11-windows-amd64.jar'));
  const extracted = path.join(output, 'postgres-archive');
  await fs.rm(extracted, {recursive: true, force: true});
  await extract(postgresJar, extracted);
  await extract(path.join(extracted, pins.postgres.member), path.join(resources, 'postgres'));
  for (const name of ['postgres', 'initdb', 'pg_ctl']) {
    const version = await run(path.join(resources, 'postgres/bin', name + '.exe'), ['--version']);
    if (!version.stdout.includes(`(PostgreSQL) ${pins.postgres.version}`)) throw new Error(`Unexpected ${name} version.`);
  }
  await fs.rm(extracted, {recursive: true, force: true});
  await fs.mkdir(path.join(resources, 'bin'));
  const backend = path.join(resources, 'bin/overkill.exe');
  await run('go', ['build', '-trimpath', '-buildvcs=false', '-ldflags', `-s -w -X main.Version=${metadata.version} -X main.Commit=${sourceCommit}`, '-o', backend, './cmd/overkill'], {env: {...process.env, CGO_ENABLED: '0', GOOS: 'windows', GOARCH: 'amd64'}});
  const goNotices = await collectGoNotices({backend, destination: path.join(resources, 'windows-go-notices'), cwd: root});
  // Electron runtime LICENSE/Chromium credits remain at the installed app root.
  // This existing notice snapshot is explicitly scoped; Windows native evidence
  // is recorded separately and does not inherit a macOS redistribution claim.
  await fs.cp(path.join(root, 'desktop/notices'), path.join(resources, 'notices'), {recursive: true});
  const windowsNotices = path.join(root, 'scripts/windows-notices');
  const noticeProvenance = JSON.parse(await fs.readFile(path.join(windowsNotices, 'provenance.json'), 'utf8'));
  if (noticeProvenance.postgresJarSHA256 !== pins.postgres.sha256) throw new Error('Windows vendor notice evidence does not match pinned PostgreSQL.');
  for (const [file, expected] of Object.entries(noticeProvenance.noticeFiles)) {
    if (await hashFile(path.join(windowsNotices, file)) !== expected) throw new Error('Windows vendor notice checksum mismatch.');
  }
  for (const [file, expected] of Object.entries(noticeProvenance.binarySHA256)) {
    if (await hashFile(path.join(resources, 'postgres', file)) !== expected) throw new Error('Windows PostgreSQL differs from vendor notice binary evidence.');
  }
  await fs.cp(windowsNotices, path.join(resources, 'windows-native-notices'), {recursive: true});
  const nativeInventory = await inventory(path.join(resources, 'postgres'));
  const manifest = {schema: 1, appVersion: metadata.version, sourceCommit, target: 'windows-amd64', electron: pins.electron, postgres: pins.postgres, backendSHA256: await hashFile(backend), signing: 'unsigned; no Windows Authenticode certificate', distributionReady: false, noticeScope: 'Electron notices from actual Windows runtime; existing application/Go notices snapshot included; Windows EDB vendor notices included with byte-for-byte match evidence for all 121 native binaries; complete source/replacement coverage requires separate review', postgresFiles: nativeInventory, goNotices};
  await fs.writeFile(path.join(resources, 'resource-manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
  const inputInventory = await inventory(stage);
  await fs.writeFile(path.join(output, 'input-inventory.json'), JSON.stringify({sourceCommit, files: inputInventory}, null, 2) + '\n');
  const innoInstaller = await download(pins.innoSetup, path.join(cache, 'innosetup-6.5.3.exe'));
  const inno = path.join(output, 'inno');
  await run(innoInstaller, ['/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/CURRENTUSER', `/DIR=${inno}`]);
  await run(path.join(inno, 'ISCC.exe'), [`/DAppVersion=${metadata.version}`, `/DStageDir=${stage}`, `/DOutputDir=${output}`, path.join(root, 'scripts/windows-desktop.iss')]);
  const installer = path.join(output, `Overkill-${metadata.version}-windows-x64-setup.exe`);
  const installerSHA256 = await hashFile(installer);
  await fs.writeFile(path.join(output, 'SHA256SUMS'), `${installerSHA256}  ${path.basename(installer)}\n`);
  const artifact = {schema: 1, version: metadata.version, sourceCommit, target: 'windows-amd64', installer: path.basename(installer), installerSHA256, electron: pins.electron, postgresVersion: pins.postgres.version, signing: manifest.signing, installedAcceptance: 'pending', distributionReady: false};
  await fs.writeFile(path.join(output, 'artifact.json'), JSON.stringify(artifact, null, 2) + '\n');
  console.log(JSON.stringify(artifact, null, 2));
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main().catch(error => {console.error(error); process.exitCode = 1;});
