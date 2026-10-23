const {app, BrowserWindow, ipcMain, dialog, safeStorage, Menu} = require('electron');
const path = require('node:path');
const fs = require('node:fs/promises');
const {allowLocalFiles} = require('./local-files.cjs');
const {Backend} = require('./backend.cjs');
const {validateCanvas} = require('./canvas.cjs');
const {validateUsageReconciliation} = require('./usage-reconciliation-input.cjs');
const {saveCanvasPNG}=require('./canvas-save.cjs');
const {captureCanvasSnapshot,cancelCanvasSnapshots} = require('./canvas-snapshot.cjs');
const {SettingsStore} = require('./settings.cjs');
const {StopMarker,haltOwnedWork}=require('./stop-marker.cjs');
const {validate: coordinationInput, methods: coordinationMethods} = require('./coordination.cjs');
const {profileDirectory} = require('./profile.cjs');

app.setName(require('../package.json').productName || 'Overkill');
try {
  const profile = profileDirectory(process.argv, {packaged: app.isPackaged});
  if (profile) {
    require('node:fs').mkdirSync(profile, {recursive: true, mode: 0o700});
    app.setPath('userData', profile);
  }
} catch (error) { console.error(error.message); app.exit(1); }
let window, backend, settings, workspace = '', busy = false, starting = true, quitting = false, quitReady = false, backendError = '';
let stopLatched=false,stopGeneration=0,stopOperation=null;
function requireWorkAdmission(){if(stopLatched)throw new Error('All agents in this app are stopped. Explicitly reconnect in Provider settings; queued requests may resume.');}
const documentPath = path.join(__dirname, 'index.html');
const isDocumentURL = allowLocalFiles([documentPath]);
const isBundledURL = allowLocalFiles(['index.html','renderer.js','history.js','workspace.js','contract-editor.js','request-review.js','request-agreements.js','style.css','canvas.js','canvas.css','canvas-panel.js','usage-reconciliation.js','clarification.js','flows.js','evolution.js'].map(name => path.join(__dirname, name)));
const emit = value => { if (window && !window.isDestroyed()) window.webContents.send('desktop:event', value); };
function trusted(event) {
  if (!window || event.sender !== window.webContents || event.senderFrame !== window.webContents.mainFrame || !isDocumentURL(event.senderFrame.url)) throw new Error('Untrusted desktop request.');
}
function register(channel, fn) {
  ipcMain.handle(channel, async (event, ...args) => { trusted(event); return fn(...args); });
}
function sessionID(value) {
  if (typeof value !== 'string' || !/^[\w-]{1,128}$/.test(value)) throw new Error('Invalid session.');
  return value;
}
async function startBackend({reconnect=false,generation=stopGeneration}={}) {
  if (quitting || (stopLatched&&!reconnect)) return;
  backendError = '';
  const saved = await settings.read();
  if (!saved || quitting || generation!==stopGeneration) return;
  await backend.start(saved);
  if(generation!==stopGeneration){await backend.stop();throw new Error('Backend startup was cancelled by Stop all agents.');}
}
async function state() {
  const publicSettings = await settings.publicSettings();
  return {ready: !!backend.credentials&&!stopLatched, starting:starting&&!stopLatched, settings: publicSettings, workspace, stopped:stopLatched, error: backendError};
}
function makeWindow() {
  window = new BrowserWindow({width: 1180, height: 820, minWidth: 840, minHeight: 600, title: 'Overkill', backgroundColor: '#151719', webPreferences: {preload: path.join(__dirname, 'preload.cjs'), sandbox: true, contextIsolation: true, nodeIntegration: false, webSecurity: true}});
  window.webContents.setWindowOpenHandler(() => ({action: 'deny'}));
  window.webContents.on('will-navigate', event => event.preventDefault());
  window.webContents.on('will-attach-webview', event => event.preventDefault());
  window.webContents.session.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
  window.webContents.session.setPermissionCheckHandler(() => false);
  window.webContents.session.webRequest.onBeforeRequest((details, callback) => {
    callback({cancel: !isBundledURL(details.url)});
  });
  window.loadFile(documentPath);
}
if (!app.requestSingleInstanceLock()) app.quit();
else {
  app.on('second-instance', () => { if (window) { if (window.isMinimized()) window.restore(); window.show(); window.focus(); } });
  app.whenReady().then(async () => {
    const home = app.getPath('userData');
    await fs.mkdir(home, {recursive: true, mode: 0o700});
    settings = new SettingsStore(home, safeStorage);
    const stopMarker=new StopMarker(home);
    try{stopLatched=await stopMarker.read();if(stopLatched)backendError='All agents in this app remain stopped. Explicitly reconnect in Provider settings; queued requests may resume.';}catch(error){stopLatched=true;backendError='Could not validate the saved stop marker. Automatic startup is blocked; explicitly reconnect in Provider settings. '+error.message;}
    const resources = app.isPackaged ? process.resourcesPath : process.env.OVERKILL_DESKTOP_RESOURCES || path.resolve(__dirname, '../../.cache/desktop-resources');
    backend = new Backend({executable: path.join(resources, 'bin', process.platform === 'win32' ? 'overkill.exe' : 'overkill'), postgresBin: path.join(resources, 'postgres/bin'), home: path.join(home, 'backend'), onExit: () => { backendError = 'The backend stopped. Open settings and reconnect to restart it.'; emit({type: 'backend_exit', message: backendError}); }});
    register('desktop:state', state);
    register('desktop:settings', async input => {
      if (quitting) throw new Error('The application is closing.');
      if(stopOperation)throw new Error('Wait for Stop all agents to finish before reconnecting.');
      const reconnectGeneration=stopGeneration;
      if (busy || backend.streams.size) throw new Error('Stop the current response before changing settings.');
      busy = true;
      try {
        await settings.save(input);
        await backend.stop();
        if(reconnectGeneration!==stopGeneration)throw new Error('Reconnect cancelled by Stop all agents.');
        await startBackend({reconnect:true,generation:reconnectGeneration});
        if(reconnectGeneration!==stopGeneration)throw new Error('Reconnect cancelled by Stop all agents.');
        try{await stopMarker.clear();}catch(error){stopLatched=true;await backend.stop();throw new Error('Reconnect stopped because the saved stop marker could not be cleared. '+error.message);}
        if(reconnectGeneration!==stopGeneration){await stopMarker.write();throw new Error('Reconnect cancelled by Stop all agents.');}
        stopLatched=false;backendError='';
        return await state();
      } catch (error) { if(!stopLatched)backendError = 'Could not start the backend. Check the provider settings and bundled database.'; throw error; }
      finally { busy = false; }
    });
    register('desktop:workspace', async () => {
      const selection = await dialog.showOpenDialog(window, {title: 'Choose workspace', properties: ['openDirectory', 'createDirectory']});
      if (!selection.canceled) workspace = await fs.realpath(selection.filePaths[0]);
      return workspace;
    });
    register('desktop:sessions', () => backend.call('session.list', {}));
    register('desktop:create', async () => {
      requireWorkAdmission();
      if (!workspace) throw new Error('Choose a workspace first.');
      const real = await fs.realpath(workspace);
      if (real !== workspace || !(await fs.stat(real)).isDirectory()) throw new Error('Workspace changed. Choose it again.');
      requireWorkAdmission();return backend.call('session.create', {folder: real});
    });
    register('desktop:messages', id => backend.call('session.messages', {id: sessionID(id)}));
    register('desktop:flows', id => backend.call('flow.list', {session_id: sessionID(id)}));
    register('desktop:evolution', input => {
      if (!input || typeof input !== 'object' || Object.keys(input).some(k => !['session_id','operation','id','approve'].includes(k))) throw new Error('Invalid project skill operation.');
      const operations = ['list','generate','review','apply','rollback'];
      if (!operations.includes(input.operation)) throw new Error('Unknown project skill operation.');
      if (input.operation !== 'list') requireWorkAdmission();
      const params = {session_id: sessionID(input.session_id)};
      if (['review','apply','rollback'].includes(input.operation)) {
        if (typeof input.id !== 'string' || !/^[a-z2-7]{26}$/.test(input.id)) throw new Error('Invalid draft ID.');
        params.id = input.id;
      }
      if (input.operation === 'review') {
        if (typeof input.approve !== 'boolean') throw new Error('Choose approve or reject.');
        params.approve = input.approve;
      }
      return backend.call('evolution.'+input.operation, params);
    });
    register('desktop:send', async input => {
      requireWorkAdmission();
      if (!input || typeof input !== 'object' || Object.keys(input).some(k => !['id', 'sessionID', 'message'].includes(k))) throw new Error('Invalid chat request.');
      sessionID(input.id); sessionID(input.sessionID);
      if (typeof input.message !== 'string' || !input.message.trim() || input.message.length > 64000) throw new Error('Enter a message of at most 64,000 characters.');
      // Reserve synchronously before IPC returns, preventing duplicate sends.
      if (!backend.credentials || backend.streams.size) throw new Error('Backend is unavailable or already responding.');
      const started=backend.startStream(input.id,input.sessionID,input.message,emit,()=>emit({id:input.id,type:'admitted',data:{sessionID:input.sessionID}}));
      void started.completion.catch(()=>emit({id:input.id,type:'error',data:{message:'Response stream failed.'}}));
      return await started.admission;
    });
    for (const [operation, method] of Object.entries(coordinationMethods)) {
      register(`desktop:coordination:${operation}`, input => {if(['register','send','review','createContract','updateContract','acknowledge'].includes(operation))requireWorkAdmission();return backend.call(method, coordinationInput(operation, input));});
    }
    for(const [operation,method]of Object.entries({read:'coordination.reconciliation',candidates:'coordination.reconciliation.candidates',write:'coordination.reconcile'}))register('desktop:usage:'+operation,input=>{if(operation==='write')requireWorkAdmission();return backend.call(method,validateUsageReconciliation(operation,input));});
    register('desktop:canvas:state', input => {
      const value=validateCanvas(input); return backend.call('canvas.state',{session_id:value.sessionID});
    });
    let exportingCanvas=false;
    register('desktop:canvas:saveSnapshot', async input => {
      requireWorkAdmission();
      const value=validateCanvas(input,true);
      if(exportingCanvas || quitting || !backend.credentials)throw new Error('Canvas export is unavailable or already running.');
      exportingCanvas=true;
      const epoch=backend.epoch;
      const alive=()=>!quitting && !stopLatched && !!backend.credentials && backend.epoch===epoch && window && !window.isDestroyed();
      try {
        const {state}=await backend.call('canvas.state',{session_id:value.sessionID});
        const surface=state.surfaces.find(item=>item.surfaceId===value.surfaceID);
        if(state.sessionId!==value.sessionID || state.revision!==value.revision)throw new Error('Canvas changed. Refresh before exporting.');
        if(!surface || !surface.rendering)throw new Error('Choose a rendered canvas surface.');
        const png=await captureCanvasSnapshot(state,value.surfaceID,{alive});
        if(!alive())throw new Error('Canvas export was cancelled.');
        const selected=await dialog.showSaveDialog(window,{title:'Save canvas PNG',defaultPath:'canvas.png',filters:[{name:'PNG image',extensions:['png']}]});
        if(selected.canceled || !selected.filePath)return {saved:false,cancelled:true,sessionID:value.sessionID,surfaceID:value.surfaceID,revision:value.revision};
        if(!alive())throw new Error('Canvas export was cancelled.');
        await saveCanvasPNG(selected.filePath,png,alive);
        return {saved:true,sessionID:value.sessionID,surfaceID:value.surfaceID,revision:value.revision};
      } finally {exportingCanvas=false;}
    });
    const {validateQuestionAnswer,validateNoInteractionInput}=require('./interaction-input.cjs');
    register('desktop:clarify:poll',id=>backend.call('clarify.poll',{session_id:sessionID(id)}));
    register('desktop:clarify:answer',input=>{requireWorkAdmission();return backend.call('agent.answer',validateQuestionAnswer(input,sessionID));});
    register('desktop:estop',input=>{
      validateNoInteractionInput(input);
      if(stopOperation)return stopOperation;
      stopLatched=true;stopGeneration++;cancelCanvasSnapshots();
      backendError='Stopping all work owned by this app. Earlier effects cannot be undone.';
      emit({type:'backend_exit',message:backendError});
      stopOperation=(async()=>{
        try{await haltOwnedWork(backend,stopMarker,async()=>{
          try{await fs.stat(path.join(home,'backend/postgres/postmaster.pid'));throw new Error('Owned PostgreSQL is still running.');}catch(error){if(error.code!=='ENOENT')throw error;}
          });
          backendError='All agents in this app are stopped. Reconnect in Provider settings to resume; queued requests may resume. Earlier effects remain. External agents are unaffected.';
          emit({type:'backend_exit',message:backendError});
          return{status:'stopped',reconnectRequired:true};
        }catch(error){backendError='Could not confirm that all owned work stopped. The stop latch remains set. '+error.message;emit({type:'backend_exit',message:backendError});throw new Error(backendError);}
        finally{stopOperation=null;}
      })();return stopOperation;
    });
    register('desktop:stop', id => backend.call('agent.abort', {session_id: sessionID(id)}));
    Menu.setApplicationMenu(Menu.buildFromTemplate([{label: 'Overkill', submenu: [{role: 'about'}, {type: 'separator'}, {role: 'quit'}]}, {label: 'Edit', submenu: [{role: 'undo'}, {role: 'redo'}, {type: 'separator'}, {role: 'cut'}, {role: 'copy'}, {role: 'paste'}, {role: 'selectAll'}]}, {label: 'Window', submenu: [{role: 'minimize'}, {role: 'zoom'}]}]));
    makeWindow();
    busy = true;
    void startBackend().catch(error => { backendError = /^(Keychain|Credential store) did not respond\./.test(error.message || '') ? error.message : 'The backend could not start. Open settings to reconnect.'; }).finally(async () => {
      starting = false; busy = false;
      try { emit({type: 'backend_state', state: await state()}); } catch { emit({type: 'backend_exit', message: 'Saved settings could not be read.'}); }
    });
    app.on('activate', () => { if (BrowserWindow.getAllWindows().length === 0) makeWindow(); });
  }).catch(() => { dialog.showErrorBox('Overkill could not start', 'The desktop application could not initialize its private data directory.'); app.quit(); });
  app.on('window-all-closed', () => app.quit());
  app.on('before-quit', event => {
    if (quitReady) return;
    event.preventDefault();
    // macOS / automation can request quit again while cleanup is pending.
    // Every request must wait for the same owned child shutdown.
    if (quitting) return;
    quitting = true;
    cancelCanvasSnapshots();
    Promise.resolve(backend?.stop()).finally(() => { quitReady = true; app.quit(); });
  });
}
