#!/usr/bin/env node
// Experimental preview artifact: ad-hoc signed, never notarized by this script.
import fs from 'node:fs/promises';
import {syncDesktopCanvas} from './desktop-canvas-assets.mjs';
import {syncDesktopTerminal} from './desktop-terminal-assets.mjs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {verifySnapshot} from './desktop-snapshot.mjs';
const exec = promisify(execFile);
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const run = (command, args) => exec(command, args, {maxBuffer: 8 * 1024 * 1024});
const hash = async file => createHash('sha256').update(await fs.readFile(file)).digest('hex');
const macho = new Set(['feedface', 'cefaedfe', 'feedfacf', 'cffaedfe', 'cafebabe', 'bebafeca', 'cafebabf', 'bfbafeca']);
async function signNativeTree(directory) {
  for (const entry of await fs.readdir(directory, {withFileTypes: true})) {
    const file = path.join(directory, entry.name);
    if (entry.isDirectory()) await signNativeTree(file);
    else if (entry.isFile()) {
      const handle = await fs.open(file, 'r');
      const magic = Buffer.alloc(4);
      try { await handle.read(magic, 0, 4, 0); } finally { await handle.close(); }
      if (macho.has(magic.toString('hex'))) await run('/usr/bin/codesign', ['--force', '--sign', '-', file]);
    }
  }
}
async function main() {
  await syncDesktopCanvas(root,{check:true});
 await syncDesktopTerminal(root,{check:true});
  if (process.platform !== 'darwin' || process.arch !== 'arm64' || process.argv.length !== 2) throw new Error('Run without arguments on macOS arm64 after prepare-desktop.mjs.');
  const metadata = JSON.parse(await fs.readFile(path.join(root, 'desktop/package.json'), 'utf8'));
  const numericVersion = /^(\d+\.\d+\.\d+)(?:-[0-9A-Za-z.-]+)?$/.exec(metadata.version)?.[1];
  if (!numericVersion) throw new Error('Desktop version must have a numeric major.minor.patch core.');
  const source = path.join(root, '.cache/desktop-resources');
  const resourceManifest = JSON.parse(await fs.readFile(path.join(source, 'resource-manifest.json'), 'utf8'));
  if (resourceManifest.target !== 'darwin-arm64' || resourceManifest.appVersion !== metadata.version || resourceManifest.electron !== metadata.devDependencies.electron || resourceManifest.backendSHA256 !== await hash(path.join(source, 'bin/overkill'))) throw new Error('Resources do not match the current desktop metadata; run prepare-desktop.mjs.');
  const electron = path.join(root, 'desktop/node_modules/electron/dist');
  if ((await fs.readFile(path.join(electron, 'version'), 'utf8')).trim() !== metadata.devDependencies.electron) throw new Error('Unexpected Electron runtime version.');
  // Inventory must be settled before notices are captured into the artifact.
  const dependencies = JSON.parse(await fs.readFile(path.join(root, 'docs/desktop-dependencies.json'), 'utf8'));
  const notices = JSON.parse(await fs.readFile(path.join(root, 'desktop/notices/manifest.json'), 'utf8'));
  if (notices.files.some(record => /\.go$/.test(record.path))) throw new Error('Copied Go source notices must use an inert .txt suffix.');
  await verifySnapshot(source, dependencies.resource_files, {exhaustive: true});
  await verifySnapshot(electron, dependencies.electron_files, {exhaustive: true});
  await verifySnapshot(root, notices.files);
  const output = path.join(root, '.cache/distribution'); await fs.mkdir(output, {recursive: true});
  const build = await fs.mkdtemp(path.join(output, `overkill-${metadata.version}-dev-arm64-`));
  const volume = path.join(build, 'volume'); await fs.mkdir(volume);
  const app = path.join(volume, 'Overkill Development.app');
  await fs.cp(path.join(electron, 'Electron.app'), app, {recursive: true, verbatimSymlinks: true});
  const contents = path.join(app, 'Contents'); const resources = path.join(contents, 'Resources');
  await fs.rm(path.join(contents, '_CodeSignature'), {recursive: true, force: true});
  await fs.rm(path.join(resources, 'default_app.asar'), {force: true});
  await fs.rename(path.join(contents, 'MacOS/Electron'), path.join(contents, 'MacOS/Overkill'));
  const plist = path.join(contents, 'Info.plist');
  for (const [key, value] of Object.entries({CFBundleDisplayName: 'Overkill Development', CFBundleName: 'Overkill Development', CFBundleExecutable: 'Overkill', CFBundleIdentifier: 'local.overkill.desktop.development', CFBundleShortVersionString: numericVersion, CFBundleVersion: numericVersion, LSMinimumSystemVersion: '13.0'})) await run('/usr/libexec/PlistBuddy', ['-c', `Set :${key} ${value}`, plist]);
  await run('/usr/libexec/PlistBuddy', ['-c', 'Delete :ElectronAsarIntegrity', plist]);
  const application = path.join(resources, 'app'); await fs.mkdir(application);
  await fs.cp(path.join(root, 'desktop/src'), path.join(application, 'src'), {recursive: true});
  await fs.writeFile(path.join(application, 'package.json'), JSON.stringify({name: metadata.name, productName: 'Overkill Development', version: metadata.version, main: metadata.main, description: metadata.description}));
  for (const item of ['bin', 'postgres', 'resource-manifest.json']) await fs.cp(path.join(source, item), path.join(resources, item), {recursive: true, verbatimSymlinks: true});
  await fs.cp(path.join(root, 'desktop/notices'), path.join(resources, 'notices'), {recursive: true});
  await fs.copyFile(path.join(root, 'docs/desktop-dependencies.json'), path.join(resources, 'dependency-inventory.json'));
  await fs.copyFile(path.join(root, 'docs/desktop-dependencies.md'), path.join(resources, 'dependency-inventory.md'));
  resourceManifest.dependencyInventoryScope = 'Validated input files before ad-hoc signing; signed-file hashes are recorded in the separate build resource-inventory.json.';
  await signNativeTree(path.join(resources, 'bin')); await signNativeTree(path.join(resources, 'postgres'));
  resourceManifest.backendSHA256 = await hash(path.join(resources, 'bin/overkill'));
  resourceManifest.signing = 'ad-hoc development only; no Developer ID or notarization';
  resourceManifest.desktopSourceFiles = await Promise.all((await fs.readdir(path.join(application, 'src'))).sort().map(async name => ({path: `src/${name}`, sha256: await hash(path.join(application, 'src', name))})));
  resourceManifest.distributionReady = false;
  await fs.writeFile(path.join(resources, 'resource-manifest.json'), JSON.stringify(resourceManifest, null, 2) + '\n');
  // Audit the signed copies: signing changes executable bytes and checksums.
  const inventory = await run('/usr/bin/python3', [path.join(root, 'scripts/audit-desktop-resources.py'), path.join(resources)]);
  await fs.writeFile(path.join(build, 'resource-inventory.json'), inventory.stdout);
  await run('/usr/bin/codesign', ['--force', '--deep', '--sign', '-', app]);
  await run('/usr/bin/codesign', ['--verify', '--deep', '--strict', app]);
  await fs.symlink('/Applications', path.join(volume, 'Applications'));
  await fs.writeFile(path.join(volume, 'READ ME.txt'), `Overkill ${metadata.version} — experimental preview\nDesktop workspace for macOS 13+ on Apple Silicon.\nAd-hoc signed; not notarized. macOS may require manual approval to open it.\nCopy Overkill Development.app to Applications or another writable folder before opening.\nThis preview is provided for testing. Distribution readiness is false: native dependency source/build provenance and licensing requirements remain unresolved.\nRead the included Resources/dependency-inventory.md and Resources/notices/README.md inside the app for the exact dependency evidence and remaining requirements.\n`);
  const dmg = path.join(build, `Overkill-${metadata.version}-development-arm64.dmg`);
  await run('/usr/bin/hdiutil', ['create', '-ov', '-volname', 'Overkill Development', '-srcfolder', volume, '-format', 'UDZO', dmg]);
  const result = {schema: 1, version: metadata.version, bundleVersion: numericVersion, releaseChannel: 'experimental-preview', target: 'darwin-arm64', minimumMacOS: '13.0', app, dmg, dmgSHA256: await hash(dmg), signing: resourceManifest.signing, distributionReady: false, installedAcceptance: 'pending'};
  await fs.writeFile(path.join(build, 'artifact.json'), JSON.stringify(result, null, 2) + '\n');
  console.log(JSON.stringify(result, null, 2));
}
main().catch(error => { console.error(error.message); process.exitCode = 1; });
