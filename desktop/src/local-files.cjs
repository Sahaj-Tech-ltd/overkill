'use strict';
const {realpathSync} = require('node:fs');
const {fileURLToPath} = require('node:url');
// Chromium and Node can spell the same Windows file with different drive case
// or short/long names. Compare exact existing files, never directory prefixes.
function allowLocalFiles(files, {realpath = realpathSync.native, platform = process.platform} = {}) {
  const canonical = file => {
    const resolved = realpath(file);
    return platform === 'win32' ? resolved.toLowerCase() : resolved;
  };
  const allowed = new Set(files.map(canonical));
  return value => {
    try {
      const url = new URL(value);
      if (url.protocol !== 'file:' || url.hostname || url.search || url.hash || url.username || url.password) return false;
      return allowed.has(canonical(fileURLToPath(url)));
    } catch { return false; }
  };
}
module.exports = {allowLocalFiles};
