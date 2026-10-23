const {test} = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const {profileDirectory} = require('../src/profile.cjs');

test('installed profiles require an explicit absolute flag and ignore development environment overrides', () => {
  const env = {OVERKILL_DESKTOP_DATA_DIR: '/tmp/development'};
  assert.equal(profileDirectory([], {packaged: true, env}), undefined);
  assert.equal(profileDirectory([], {packaged: false, env}), path.resolve('/tmp/development'));
  const chosen = path.resolve('/tmp', 'App Data With Spaces');
  assert.equal(profileDirectory([`--overkill-profile=${chosen}`], {packaged: true, env}), chosen);
  for (const args of [['--overkill-profile'], ['--overkill-profile='], ['--overkill-profile=relative'], ['--overkill-profile=/a\0b'], ['--overkill-profile=/a', '--overkill-profile=/b']]) {
    assert.throws(() => profileDirectory(args, {packaged: true, env}), /overkill-profile/);
  }
});
