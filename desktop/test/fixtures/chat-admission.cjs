// Disposable latency adapter only; does not change HTTP admission or stream events.
const {Backend}=require('../../src/backend.cjs');const start=Backend.prototype.startStream;
Backend.prototype.startStream=function(...args){const stream=start.apply(this,args);return{completion:stream.completion,admission:stream.admission.then(async value=>{if(globalThis.holdAdmissionReply)await new Promise(resolve=>{globalThis.releaseAdmissionReply=resolve;});return value;})};};
require('./interactive.cjs');
