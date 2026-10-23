const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {pathToFileURL} = require('node:url');
const {allowLocalFiles} = require('../src/local-files.cjs');
test('local request whitelist accepts exact existing files and rejects other resources and URL suffixes', t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'Overkill Files '));
  t.after(() => fs.rmSync(root, {recursive:true, force:true}));
  const file = path.join(root, 'index.html'); fs.writeFileSync(file, 'fixture');
  const other = path.join(root, 'other.html'); fs.writeFileSync(other, 'other');
  const allow = allowLocalFiles([file]); const url = pathToFileURL(file).href;
  assert.equal(allow(url), true);
  for (const denied of [pathToFileURL(other).href,url+'?query',url+'#fragment','https://example.com/','file://example.com/index.html','not a URL']) assert.equal(allow(denied), false, denied);
});
test('Windows aliases use canonical file identity without permitting sibling files', () => {
  const original = path.resolve('App/index.html');
  const short = path.resolve('Short/index.html');
  const differentCase = path.resolve('Case/index.html');
  const allow = allowLocalFiles([original], {platform:'win32',realpath(file) {
    if (file === original || file === short) return 'C:/Long App/index.html';
    if (file === differentCase) return 'c:/long app/INDEX.HTML';
    return 'C:/Long App/other.html';
  }});
  assert.equal(allow(pathToFileURL(short).href), true);
  assert.equal(allow(pathToFileURL(differentCase).href), true);
  assert.equal(allow(pathToFileURL(path.resolve('Short/other.html')).href), false);
});
