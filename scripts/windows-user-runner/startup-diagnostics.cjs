// Test-only observation; preserve original methods and all return values.
const {app, dialog} = require('electron');
const quit = app.quit.bind(app);
app.quit = (...args) => {console.error('acceptance app.quit', new Error().stack); return quit(...args);};
const lock = app.requestSingleInstanceLock.bind(app);
app.requestSingleInstanceLock = (...args) => {const result = lock(...args); console.error('acceptance single-instance lock', result, 'profile', app.getPath('userData')); return result;};
const errorBox = dialog.showErrorBox.bind(dialog);
dialog.showErrorBox = (...args) => {console.error('acceptance startup error box', ...args); return errorBox(...args);};
process.on('uncaughtExceptionMonitor', error => console.error('acceptance uncaught error', error.stack));
app.on('ready', () => console.error('acceptance app ready'));
app.on('browser-window-created', () => console.error('acceptance browser window created'));
app.on('window-all-closed', () => console.error('acceptance all windows closed'));
