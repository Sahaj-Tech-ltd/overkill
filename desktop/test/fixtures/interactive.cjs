// Disposable local acceptance only, excluded from packaged application sources.
// Credentials stay in the test main process; never exposed to the renderer.
const {Backend}=require('../../src/backend.cjs');const start=Backend.prototype.start;
Backend.prototype.start=async function(...args){const result=await start.apply(this,args);globalThis.interactiveFixtureCredentials=()=>this.credentials?{...this.credentials}:null;return result;};
require('./workspace.cjs');
