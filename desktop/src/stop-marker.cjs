'use strict';
const fs=require('node:fs/promises');const path=require('node:path');const {atomicWrite}=require('./settings.cjs');
class StopMarker{
 constructor(home){this.file=path.join(home,'stop-latch.json');}
 async read(){try{const stat=await fs.lstat(this.file);if(!stat.isFile()||stat.size>1024)throw new Error('Invalid stop marker.');const value=JSON.parse(await fs.readFile(this.file,'utf8'));if(!value||Object.keys(value).sort().join(',')!=='stopped,version'||value.version!==1||value.stopped!==true)throw new Error('Invalid stop marker.');return true;}catch(error){if(error.code==='ENOENT')return false;throw error;}}
 write(){return atomicWrite(this.file,JSON.stringify({version:1,stopped:true})+'\n');}
 clear(){return fs.rm(this.file,{force:true});}
}
async function haltOwnedWork(backend,marker,verifyStopped){
 // Start draining immediately, and never abandon it because persistence fails.
 const [drain,persist]=await Promise.allSettled([backend.stop(),marker.write()]);
 if(drain.status==='rejected')throw new Error('Owned backend stop failed: '+drain.reason.message);
 await verifyStopped();
 if(persist.status==='rejected')throw new Error('Owned work stopped, but the stop marker could not be saved; app restart may resume queued work. '+persist.reason.message);
}
module.exports={StopMarker,haltOwnedWork};
