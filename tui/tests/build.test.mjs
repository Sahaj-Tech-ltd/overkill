import assert from 'node:assert/strict';
import { readdir, readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import test from 'node:test';

const dist = new URL('../dist/', import.meta.url);

// Import the entire emitted graph using plain Node (no tsx loader). This
// catches unresolved .ts/.tsx paths even in components not mounted at startup.
test('compiled terminal UI modules load with Node', async () => {
  let count = 0;
  for (const entry of await readdir(dist, { recursive: true })) {
    if (!entry.endsWith('.js')) continue;
    const url = new URL(entry, dist);
    const source = await readFile(url, 'utf8');
    assert.doesNotMatch(source, /(?:from\s*|import\s*\()["'][^"']+\.tsx?["']/);
    if (entry === 'entry.js') {
      // The entry point starts an interactive application. Parse it without
      // mounting so this test remains suitable for CI without a terminal.
      const result = spawnSync(process.execPath, ['--check', fileURLToPath(url)], { encoding: 'utf8' });
      assert.equal(result.status, 0, result.stderr);
    } else {
      await import(url.href);
    }
    count++;
  }
  assert.ok(count > 1, 'build must emit the UI, not just declarations');
});
