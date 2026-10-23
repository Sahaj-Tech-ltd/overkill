const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const {createHash} = require('node:crypto');

test('packaging rejects replaced dependency bytes and retargeted license symlinks', async t => {
  const {verifySnapshot} = await import('../../scripts/desktop-snapshot.mjs');
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'overkill-snapshot-'));
  t.after(() => fs.rm(root, {recursive: true, force: true}));
  await fs.writeFile(path.join(root, 'license'), 'original');
  const file = {path: 'license', kind: 'file', bytes: 8, sha256: createHash('sha256').update('original').digest('hex')};
  await verifySnapshot(root, [file]);
  await fs.writeFile(path.join(root, 'unlisted-executable'), 'unexpected');
  await assert.rejects(verifySnapshot(root, [file], {exhaustive: true}), /Uninventoried dependency/);
  await fs.unlink(path.join(root, 'unlisted-executable'));
  await fs.writeFile(path.join(root, 'license'), 'modified');
  await assert.rejects(verifySnapshot(root, [file]), /checksum changed/);
  await assert.rejects(verifySnapshot(root, [{...file, path: '../outside'}]), /Invalid inventory path/);
  // Windows development hosts may require elevated symlink privileges.
  if (process.platform !== 'win32') {
    await fs.symlink('license', path.join(root, 'link'));
    const link = {path: 'link', kind: 'symlink', target: 'license'};
    await verifySnapshot(root, [link]);
    await fs.unlink(path.join(root, 'link')); await fs.symlink('other', path.join(root, 'link'));
    await assert.rejects(verifySnapshot(root, [link]), /symlink changed/);
  }
});
