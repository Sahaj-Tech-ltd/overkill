import fs from 'node:fs/promises';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
const exec = promisify(execFile);
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const escaped = value => value.replace(/[A-Z]/g, char => '!' + char.toLowerCase());
const noticeName = /^(licen[cs]e|copying|notice|copyright|authors|patents)([._-].*)?$/i;

// Metadata comes from the actual final backend, never go.mod's larger graph.
// Module ZIPs are inert data; no source from them is executed.
export async function collectGoNotices({backend, destination, cwd, go = 'go', tar = process.platform === 'win32' ? 'tar.exe' : 'tar'}) {
  const run = (command, args) => exec(command, args, {cwd, windowsHide: true, maxBuffer: 16 * 1024 * 1024});
  const build = JSON.parse((await run(go, ['version', '-m', '-json', backend])).stdout);
  const cache = (await run(go, ['env', 'GOMODCACHE'])).stdout.trim();
  if (!Array.isArray(build.Deps) || !build.Deps.length) throw new Error('Backend has no linked module metadata.');
  const parent = path.dirname(destination); await fs.mkdir(parent, {recursive: true});
  const stage = await fs.mkdtemp(path.join(parent, '.windows-go-notices-'));
  const modules = [];
  try {
    for (const dependency of build.Deps) {
      const {Path: module, Version: version, Sum: sum, Replace: replacement} = dependency;
      if (replacement || !/^[a-zA-Z0-9!._~+\-/]+$/.test(module) || !/^v[0-9][a-zA-Z0-9.+-]*$/.test(version) || !/^h1:[a-zA-Z0-9+/=]+$/.test(sum)) throw new Error('Unsupported linked module provenance.');
      const archive = path.join(cache, 'cache/download', escaped(module), '@v', version + '.zip');
      if ((await fs.readFile(archive.slice(0, -4) + '.ziphash', 'utf8')).trim() !== sum) throw new Error(`Cached module checksum differs from backend: ${module}`);
      const archiveSHA256 = hash(await fs.readFile(archive));
      const prefix = module + '@' + version + '/';
      const listed = (await run(tar, ['-tf', archive])).stdout.split(/\r?\n/).filter(Boolean);
      if (listed.length > 100000) throw new Error('Module archive entry bound exceeded.');
      const members = listed.filter(member => member.startsWith(prefix) && !member.endsWith('/') && noticeName.test(path.posix.basename(member)));
      const records = [];
      const scratch = path.join(stage, '.extract'); await fs.mkdir(scratch);
      for (const member of members) {
        const relative = member.slice(prefix.length);
        if (relative.startsWith('/') || relative.includes('\\') || relative.split('/').some(part => part === '..' || part.includes(':')) || /[\x00-\x1f]/.test(relative)) throw new Error('Unsafe module notice member.');
        // Extract only the identified member. Each module gets fresh scratch;
        // the package inventory later rejects nonregular packaged entries.
        await run(tar, ['-xf', archive, '-C', scratch, '--', member]);
        const input = path.join(scratch, member);
        if (!(await fs.lstat(input)).isFile()) throw new Error('Module notice must be a regular file.');
        const bytes = await fs.readFile(input);
        if (bytes.length > 8 * 1024 * 1024) throw new Error('Module notice size bound exceeded.');
        const inert = /\.go$/i.test(relative) ? relative + '.txt' : relative;
        const output = module.replaceAll('/', '__') + '@' + version + '/' + inert;
        const target = path.join(stage, output); await fs.mkdir(path.dirname(target), {recursive: true}); await fs.writeFile(target, bytes);
        records.push({path: output, archiveMember: member, bytes: bytes.length, sha256: hash(bytes)});
      }
      await fs.rm(scratch, {recursive: true});
      if (!records.some(record => !record.archiveMember.slice(prefix.length).includes('/'))) throw new Error(`No root license/notice in linked module: ${module}`);
      modules.push({module, version, goSum: sum, cacheGoSumMatchesBinary: true, archiveSHA256, sourceURL: `https://proxy.golang.org/${escaped(module)}/@v/${version}.zip`, noticeFiles: records});
    }
    const manifest = {schema: 1, goVersion: build.GoVersion, backendSHA256: hash(await fs.readFile(backend)), modules, scope: 'Exact linked backend module ZIP notices including nested notices; complete corresponding-source obligations not asserted'};
    await fs.writeFile(path.join(stage, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
    await fs.rm(destination, {recursive: true, force: true}); await fs.rename(stage, destination);
    return manifest;
  } catch (error) { await fs.rm(stage, {recursive: true, force: true}); throw error; }
}
