const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const os=require('node:os');
const path=require('node:path');
const {saveCanvasPNG}=require('../src/canvas-save.cjs');
test('cancelled canvas save preserves an existing file and removes staged bytes',async t=>{
 const directory=await fs.mkdtemp(path.join(os.tmpdir(),'overkill-save-'));t.after(()=>fs.rm(directory,{recursive:true,force:true}));const target=path.join(directory,'Existing.png');await fs.writeFile(target,'old');
 await assert.rejects(saveCanvasPNG(target,Buffer.from('new'),()=>false),/cancelled/);assert.equal(await fs.readFile(target,'utf8'),'old');assert.deepEqual(await fs.readdir(directory),['Existing.png']);
 await saveCanvasPNG(target,Buffer.from('new'));assert.equal(await fs.readFile(target,'utf8'),'new');assert.deepEqual(await fs.readdir(directory),['Existing.png']);
});
