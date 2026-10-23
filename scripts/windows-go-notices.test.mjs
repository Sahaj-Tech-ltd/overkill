import {test} from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {fileURLToPath} from 'node:url';
import {collectGoNotices} from './windows-go-notices.mjs';
const exec = promisify(execFile);
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

test('actual Windows-linked wincred PE yields exact cached module notices', {timeout: 120000}, async () => {
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-windows-notices-'));
  try {
    const source = path.join(temp, 'main.go'), backend = path.join(temp, 'probe.exe');
    await fs.writeFile(source, 'package main\nimport "github.com/danieljoos/wincred"\nfunc main(){ _,_ = wincred.GetGenericCredential("disposable-not-executed-fixture") }\n');
    await exec('go', ['build', '-trimpath', '-buildvcs=false', '-o', backend, source], {cwd: root, windowsHide: true, env: {...process.env, GOOS: 'windows', GOARCH: 'amd64', CGO_ENABLED: '0'}});
    const destination = path.join(temp, 'notices');
    const manifest = await collectGoNotices({backend, destination, cwd: root});
    const wincred = manifest.modules.find(module => module.module === 'github.com/danieljoos/wincred');
    assert.ok(wincred, 'Windows-specific linked dependency must not inherit a macOS inventory');
    assert.equal(wincred.cacheGoSumMatchesBinary, true);
    assert.match(wincred.archiveSHA256, /^[0-9a-f]{64}$/);
    assert.ok(wincred.sourceURL.endsWith('/' + wincred.version + '.zip'));
    const license = wincred.noticeFiles.find(file => file.archiveMember.endsWith('/LICENSE'));
    assert.ok(license);
    assert.match(await fs.readFile(path.join(destination, license.path), 'utf8'), /Permission is hereby granted/);
    const stored = JSON.parse(await fs.readFile(path.join(destination, 'manifest.json'), 'utf8'));
    assert.deepEqual(stored, manifest);
    assert.ok(manifest.modules.every(module => module.noticeFiles.length > 0));
    assert.ok(manifest.modules.flatMap(module => module.noticeFiles).every(file => !file.path.endsWith('.go')));
  } finally { await fs.rm(temp, {recursive: true, force: true}); }
});
