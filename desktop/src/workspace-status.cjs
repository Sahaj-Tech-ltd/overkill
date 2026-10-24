const {execFile} = require('node:child_process');
const {promisify} = require('node:util');
const run = promisify(execFile);
async function workspaceStatus(folder) {
  const git = async args => (await run('git', ['-C', folder, '--no-optional-locks', '-c', 'core.fsmonitor=false', ...args], {timeout: 5000, maxBuffer: 2 * 1024 * 1024, encoding: 'utf8', windowsHide: true})).stdout;
  try {
    const [raw, branch, files] = await Promise.all([git(['status', '--porcelain=v1', '-z', '--untracked-files=normal']), git(['branch', '--show-current']), git(['ls-files', '-z', '--cached', '--others', '--exclude-standard'])]);
    const records = raw.split('\0'); const changes = [];
    for (let i = 0; i < records.length; i++) {
      if (!records[i]) continue;
      const status = records[i].slice(0, 2), name = records[i].slice(3);
      const original = /[RC]/.test(status) ? records[++i] : undefined;
      changes.push({status, path: name, ...(original ? {original} : {})});
    }
    return {available: true, branch: branch.trim() || 'Detached HEAD', changes, files: [...new Set(files.split('\0').filter(Boolean))]};
  } catch { return {available: false, message: 'Git information is unavailable for this folder.', changes: [], files: []}; }
}
module.exports = {workspaceStatus};
