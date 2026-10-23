'use strict';
const {BrowserWindow,nativeImage} = require('electron');
const path = require('node:path');
const {allowLocalFiles} = require('./local-files.cjs');
const active = new Set();
function cancelCanvasSnapshots() { for (const window of active) if (!window.isDestroyed()) window.destroy(); }
async function captureCanvasSnapshot(state, surfaceID, {alive = () => true, width = 960, height = 640} = {}) {
  if (!Number.isInteger(width) || width<240 || width>1920 || !Number.isInteger(height) || height<160 || height>1440 || Buffer.byteLength(JSON.stringify(state))>256*1024) throw new Error('Canvas capture exceeds supported bounds.');
  if (active.size) throw new Error('A canvas export is already running.');
  const capture = new BrowserWindow({show:false,width,height,useContentSize:true,webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false,webSecurity:true,backgroundThrottling:false,partition:`canvas-capture-${require('node:crypto').randomUUID()}`}});
  active.add(capture);
  const allowed = allowLocalFiles(['canvas-snapshot.html','canvas-snapshot.js','canvas-snapshot.css','canvas.js','canvas.css'].map(name => path.join(__dirname,name)));
  capture.webContents.setWindowOpenHandler(() => ({action:'deny'}));
  capture.webContents.on('will-navigate', event => event.preventDefault());
  capture.webContents.on('will-attach-webview', event => event.preventDefault());
  capture.webContents.session.setPermissionRequestHandler((_contents,_permission,callback) => callback(false));
  capture.webContents.session.setPermissionCheckHandler(() => false);
  capture.webContents.session.webRequest.onBeforeRequest((details,callback) => callback({cancel:!allowed(details.url)}));
  let timeout;
  try {
    return await Promise.race([(async () => {
      if (!alive()) throw new Error('Canvas export was cancelled.');
      await capture.loadFile(path.join(__dirname,'canvas-snapshot.html'));
      const encoded = Buffer.from(JSON.stringify(state)).toString('base64');
      const identity = await capture.webContents.executeJavaScript(`window.renderCanvasSnapshot(${JSON.stringify(encoded)},${JSON.stringify(surfaceID)})`);
      if (!alive() || identity.revision!==state.revision || identity.surfaceID!==surfaceID) throw new Error('Canvas changed during export. Refresh and retry.');
      const image = await capture.webContents.capturePage({x:0,y:0,width,height});
      if (!alive() || image.isEmpty()) throw new Error('Canvas capture failed or was cancelled.');
      // CapturePage follows the display backing scale. Decode its actual pixels
      // and resize explicitly so exported PNG dimensions are host-independent.
      const png = nativeImage.createFromBuffer(image.toPNG(),{scaleFactor:1}).resize({width,height,quality:'best'}).toPNG();
      if (png.length>12*1024*1024) throw new Error('Canvas capture exceeds the export limit.');
      return png;
    })(),new Promise((_,reject) => { timeout=setTimeout(() => {if(!capture.isDestroyed())capture.destroy();reject(new Error('Canvas capture timed out.'));},15000); })]);
  } finally { clearTimeout(timeout); active.delete(capture); if (!capture.isDestroyed()) capture.destroy(); }
}
module.exports = {captureCanvasSnapshot,cancelCanvasSnapshots};
