// Test-only, isolated profile. Exercise genuine exhaustion without waiting for
// 40 model steps; production settings and runtime code are unchanged.
const {Backend} = require('../../src/backend.cjs');
const start = Backend.prototype.start;
Backend.prototype.start = async function(...args) {
  const write = this.writeConfig;
  this.writeConfig = (file, contents) => write(file, contents.replace('max_turns = 40', 'max_turns = 1'));
  try { return await start.apply(this, args); } finally { this.writeConfig = write; }
};
require('./interactive.cjs');
