import fs from 'node:fs/promises';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {createReadStream} from 'node:fs';

export async function verifySnapshot(root, records, {exhaustive = false} = {}) {
  root = path.resolve(root);
  for (const record of records) {
    const target = path.resolve(root, record.path);
    const relative = path.relative(root, target);
    if (!relative || relative === '..' || relative.startsWith('..' + path.sep) || path.isAbsolute(relative)) throw new Error(`Invalid inventory path: ${record.path}`);
    const stat = await fs.lstat(target);
    if (record.kind === 'symlink') {
      if (!stat.isSymbolicLink() || await fs.readlink(target) !== record.target) throw new Error(`Inventory symlink changed: ${record.path}`);
    } else if (record.kind === 'file') {
      if (!stat.isFile() || stat.size !== record.bytes) throw new Error(`Inventory file changed: ${record.path}`);
      const digest = createHash('sha256');
      for await (const chunk of createReadStream(target)) digest.update(chunk);
      if (digest.digest('hex') !== record.sha256) throw new Error(`Inventory checksum changed: ${record.path}`);
    } else throw new Error(`Unsupported inventory record: ${record.path}`);
  }
  if (exhaustive) {
    const known = new Set(records.map(record => path.resolve(root, record.path)));
    async function walk(directory) {
      for (const entry of await fs.readdir(directory, {withFileTypes: true})) {
        const file = path.join(directory, entry.name);
        if (entry.isDirectory()) await walk(file);
        else if (!known.has(file)) throw new Error(`Uninventoried dependency file: ${path.relative(root, file)}`);
      }
    }
    await walk(root);
  }
}
