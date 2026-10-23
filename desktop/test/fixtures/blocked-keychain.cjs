// Controlled acceptance fixture: no real credentials or Keychain permission.
const {safeStorage} = require('electron');
safeStorage.isAsyncEncryptionAvailable = () => new Promise(resolve => { globalThis.releaseKeychainFixture = resolve; });
safeStorage.decryptStringAsync = async () => { throw new Error('late decryption must not run'); };
require('../../src/main.cjs');
