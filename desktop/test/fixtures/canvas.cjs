// Disposable acceptance adapter. Production exposes no generic RPC or delays.
const {Backend}=require('../../src/backend.cjs');
const call=Backend.prototype.call;
Backend.prototype.call=async function(method,params){
  const result=await call.call(this,method,params);
  if(method==='canvas.state' && globalThis.canvasFixtureHoldSession===params.session_id){
    globalThis.canvasFixtureHoldSession=null;
    await new Promise(resolve=>{globalThis.canvasFixtureRelease=resolve;});
  }
  return result;
};
require('./workspace.cjs');
