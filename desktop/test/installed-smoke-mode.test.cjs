const {test} = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const {spawnSync} = require('node:child_process');

// These invalid combinations must stop before Electron launch, including when
// ordinary optional native tests would otherwise be skipped.
for (const [name, required, executable] of [
  ['missing installed executable', '1', ''],
  ['relative installed executable', '1', './Overkill'],
  ['missing mandatory test mode', '', '/unused/Overkill.app/Contents/MacOS/Overkill'],
]) {
  test(`installed mock storage smoke refuses ${name}`, () => {
    const env = {...process.env, OVERKILL_INSTALLED_MOCK_STORAGE_SMOKE: '1', OVERKILL_REQUIRE_DESKTOP_TEST: required, OVERKILL_INSTALLED_DESKTOP_EXECUTABLE: executable};
    delete env.NODE_TEST_CONTEXT;
    const child = spawnSync(process.execPath, ['--test', path.join(__dirname, 'native.test.cjs')], {
      env,
      encoding: 'utf8', timeout: 10000,
    });
    assert.equal(child.error, undefined);
    assert.notEqual(child.status, 0);
    assert.match(child.stdout + child.stderr, /Mock storage smoke requires an explicit installed executable and mandatory desktop test mode/);
  });
}
