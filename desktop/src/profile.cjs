const path = require('node:path');

// An explicit command-line profile supports separate workspaces and installed
// acceptance without relying on development-only environment overrides.
function profileDirectory(argv, {packaged, env = process.env} = {}) {
  const flags = argv.filter(arg => arg === '--overkill-profile' || arg.startsWith('--overkill-profile='));
  if (flags.length > 1) throw new Error('Specify --overkill-profile only once.');
  if (flags.length) {
    const value = flags[0].slice('--overkill-profile='.length);
    if (!flags[0].startsWith('--overkill-profile=') || !value || value.includes('\0') || !path.isAbsolute(value)) throw new Error('--overkill-profile requires an absolute directory: --overkill-profile=/path/to/profile');
    return path.resolve(value);
  }
  return !packaged && env.OVERKILL_DESKTOP_DATA_DIR ? path.resolve(env.OVERKILL_DESKTOP_DATA_DIR) : undefined;
}
module.exports = {profileDirectory};
