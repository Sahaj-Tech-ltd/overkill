import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const {_electron} = createRequire(path.join(root, 'desktop/package.json'))('playwright');
const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'Overkill Ordinary Preflight '));
let app;
try {
  app = await _electron.launch({executablePath: path.join(root, 'desktop/node_modules/electron/dist/electron.exe'), args: ['-r', path.join(root, 'scripts/windows-user-runner/startup-diagnostics.cjs'), path.join(root, 'desktop'), `--overkill-profile=${profile}`], timeout: 30000});
  await app.firstWindow();
  const result = await app.evaluate(async ({safeStorage}) => {
    if (!await safeStorage.isAsyncEncryptionAvailable()) throw new Error('Native credential encryption unavailable');
    const encrypted = await safeStorage.encryptStringAsync('disposable-preflight');
    return (await safeStorage.decryptStringAsync(encrypted)).result === 'disposable-preflight';
  });
  if (!result) throw new Error('Native preflight encryption roundtrip failed');
  console.log('PASS ordinary-user native Electron GUI and real encryption preflight');
} finally {
  if (app) {let timer; try {await Promise.race([app.close(), new Promise((_, reject) => {timer=setTimeout(()=>reject(new Error('preflight close timed out')),10000);})]);} finally {clearTimeout(timer);}}
  await fs.rm(profile, {recursive:true,force:true,maxRetries:2});
}
