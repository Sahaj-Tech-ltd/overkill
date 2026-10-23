#!/usr/bin/env node
// Prepare development resources. Release packaging additionally requires the
// installed-app acceptance gate and complete dependency/license inventory.
import fs from 'node:fs/promises';
import {syncDesktopCanvas} from './desktop-canvas-assets.mjs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';

const exec = promisify(execFile);
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const cache = path.join(root, '.cache/desktop-vendor');
const output = path.join(root, '.cache/desktop-resources');
const postgres = {
  version: '17.11',
  url: 'https://repo.maven.apache.org/maven2/io/zonky/test/postgres/embedded-postgres-binaries-darwin-arm64v8/17.11.0/embedded-postgres-binaries-darwin-arm64v8-17.11.0.jar',
  sha256: 'a1c2786acb0c398f9b2d76806fc52f5dc8b222cbc8e9383a9b9702084daaf3a5',
};
const hash = data => createHash('sha256').update(data).digest('hex');

async function pinnedDownload() {
  const artifact = path.join(cache, 'postgres-17.11-darwin-arm64.jar');
  let bytes;
  try { bytes = await fs.readFile(artifact); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
  if (!bytes || hash(bytes) !== postgres.sha256) {
    const response = await fetch(postgres.url, {signal: AbortSignal.timeout(120000)});
    if (!response.ok) throw new Error(`PostgreSQL download failed: HTTP ${response.status}`);
    bytes = Buffer.from(await response.arrayBuffer());
    if (hash(bytes) !== postgres.sha256) throw new Error('PostgreSQL artifact checksum mismatch.');
    const temporary = artifact + `.${process.pid}.tmp`;
    await fs.writeFile(temporary, bytes, {mode: 0o600, flag: 'wx'});
    await fs.rename(temporary, artifact);
  }
  return artifact;
}

// This artifact also contains optional server languages linked to an EDB
// installation under /Library. Overkill does not use those extensions. Omit
// their modules and SQL/control advertisements together instead of shipping
// features that fail to load on a clean Mac.
async function omitUnsupportedExtensions(native) {
  const extensions = new Set(['bool_plperl', 'hstore_plperl', 'jsonb_plperl', 'plperl', 'plperlu', 'hstore_plpython3u', 'jsonb_plpython3u', 'ltree_plpython3u', 'plpython3u', 'pltcl', 'pltclu', 'xml2']);
  const modules = ['bool_plperl', 'hstore_plperl', 'jsonb_plperl', 'plperl', 'hstore_plpython3', 'jsonb_plpython3', 'ltree_plpython3', 'plpython3', 'pltcl', 'pgxml'];
  const omitted = [];
  const extensionDir = path.join(native, 'share/postgresql/extension');
  for (const name of await fs.readdir(extensionDir)) {
    if ([...extensions].some(extension => name === extension + '.control' || name.startsWith(extension + '--'))) {
      await fs.unlink(path.join(extensionDir, name)); omitted.push('share/postgresql/extension/' + name);
    }
  }
  for (const module of modules) {
    const relative = 'lib/postgresql/' + module + '.dylib';
    await fs.unlink(path.join(native, relative)); omitted.push(relative);
  }
  return {reason: 'Optional EDB language-pack or absent libxslt dependencies; not used by Overkill.', files: omitted.sort()};
}

async function main() {
  if (process.platform !== 'darwin' || process.arch !== 'arm64') throw new Error('This desktop resource target currently requires macOS arm64.');
  if (process.argv.length !== 2) throw new Error('Usage: node scripts/prepare-desktop.mjs');
  await syncDesktopCanvas(root);
  await fs.mkdir(cache, {recursive: true}); await fs.mkdir(output, {recursive: true});
  const artifact = await pinnedDownload();
  const stage = await fs.mkdtemp(path.join(cache, 'extract-'));
  let omittedExtensions;
  try {
    const archive = path.join(stage, 'postgres.txz');
    const {stdout} = await exec('/usr/bin/unzip', ['-p', artifact, 'postgres-darwin-arm_64.txz'], {encoding: 'buffer', maxBuffer: 128 * 1024 * 1024});
    await fs.writeFile(archive, stdout);
    const native = path.join(stage, 'postgres'); await fs.mkdir(native);
    await exec('/usr/bin/tar', ['-xf', archive, '-C', native], {maxBuffer: 1024 * 1024});
    const version = await exec(path.join(native, 'bin/postgres'), ['--version']);
    if (!version.stdout.includes(`(PostgreSQL) ${postgres.version}`)) throw new Error('Unexpected bundled PostgreSQL version.');
    omittedExtensions = await omitUnsupportedExtensions(native);
    // Publish a fresh verified tree; do not modify binaries in a running tree.
    const destination = path.join(output, 'postgres');
    const previous = path.join(cache, `previous-postgres-${process.pid}`);
    let moved = false;
    try { await fs.rename(destination, previous); moved = true; } catch (error) { if (error.code !== 'ENOENT') throw error; }
    try { await fs.rename(native, destination); }
    catch (error) { if (moved) await fs.rename(previous, destination); throw error; }
    if (moved) await fs.rm(previous, {recursive: true});
  } finally { await fs.rm(stage, {recursive: true, force: true}); }

  const metadata = JSON.parse(await fs.readFile(path.join(root, 'desktop/package.json'), 'utf8'));
  const localGo = path.join(root, '.cache/toolchains/go/bin/go');
  const go = process.env.OVERKILL_GO || await fs.access(localGo).then(() => localGo, () => 'go');
  await fs.mkdir(path.join(output, 'bin'), {recursive: true});
  const binary = path.join(output, 'bin/overkill'); const temporary = binary + `.${process.pid}.tmp`;
  try {
    await exec(go, ['build', '-trimpath', '-buildvcs=false', '-ldflags', `-X main.Version=${metadata.version}-desktop-dev`, '-o', temporary, './cmd/overkill'], {cwd: root, env: {...process.env, CGO_ENABLED: '0'}, maxBuffer: 1024 * 1024});
    await fs.rename(temporary, binary);
  } finally { await fs.rm(temporary, {force: true}); }
  await fs.writeFile(path.join(output, 'resource-manifest.json'), JSON.stringify({schema: 1, appVersion: metadata.version, target: 'darwin-arm64', postgres, omittedExtensions, electron: metadata.devDependencies.electron, backendSHA256: hash(await fs.readFile(binary)), distributionReady: false}, null, 2)+'\n');
  console.log(`Prepared verified desktop development resources: ${output}`);
}
main().catch(error => { console.error(error.message); process.exitCode = 1; });
