import {test} from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {safeArchiveEntries, sha256} from './windows-package-desktop.mjs';

test('archive inventory rejects Windows and POSIX extraction escapes', () => {
  for (const invalid of ['../outside', '/absolute', 'C:/absolute', 'C:relative', 'bin/../../outside', 'bin\\..\\outside', 'bin/unsafe\0file', 'bin/stream:alternate', '']) {
    assert.throws(() => safeArchiveEntries(invalid));
  }
  assert.deepEqual(safeArchiveEntries('bin/postgres.exe\r\nshare/postgres.bki\r\n'), ['bin/postgres.exe', 'share/postgres.bki']);
});

test('pinned Windows vendor evidence and notices bind actual bundled binary family', async () => {
  const pins = JSON.parse(await fs.readFile(new URL('./windows-desktop-pins.json', import.meta.url)));
  assert.equal(pins.electron, '45.0.0');
  assert.match(pins.postgres.sha256, /^[0-9a-f]{64}$/);
  assert.match(pins.innoSetup.sha256, /^[0-9a-f]{64}$/);
  const proof = JSON.parse(await fs.readFile(new URL('./windows-notices/provenance.json', import.meta.url)));
  assert.equal(proof.postgresJarSHA256, pins.postgres.sha256);
  assert.equal(proof.binaryExactMatches.length, 121);
  assert.deepEqual(proof.binaryDifferencesOrAbsent, []);
  for (const [name, expected] of Object.entries(proof.noticeFiles)) assert.equal(sha256(await fs.readFile(fileURLToPath(new URL('./windows-notices/' + name, import.meta.url)))), expected);
  for (const name of ['bin/postgres.exe', 'bin/initdb.exe', 'bin/pg_ctl.exe']) assert.match(proof.binarySHA256[name], /^[0-9a-f]{64}$/);
});
