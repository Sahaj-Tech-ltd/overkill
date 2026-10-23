const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const {validateCanvas}=require('../src/canvas.cjs');
test('canvas IPC accepts only named session/surface/revision input',()=>{
 assert.deepEqual(validateCanvas({sessionID:'session-a'}),{sessionID:'session-a'});
 assert.deepEqual(validateCanvas({sessionID:'session-a',surfaceID:'main',revision:4},true),{sessionID:'session-a',surfaceID:'main',revision:4});
 for(const invalid of [null,[],{}, {sessionID:'a',method:'canvas.apply'}, {sessionID:'../a'}, {sessionID:'a',path:'/tmp/x'}])assert.throws(()=>validateCanvas(invalid));
 for(const invalid of [{sessionID:'a',surfaceID:'main',revision:1,html:'x'},{sessionID:'a',surfaceID:'main',revision:-1},{sessionID:'a',surfaceID:'main',revision:1.2},{sessionID:'a',surfaceID:' main',revision:1},{sessionID:'a',surfaceID:'x\ny',revision:1}])assert.throws(()=>validateCanvas(invalid,true));
});
test('desktop renderer and CSS exactly match canonical canvas assets',async()=>{
 for(const name of ['canvas.js','canvas.css'])assert.deepEqual(await fs.readFile(path.resolve(__dirname,'../../internal/web/static',name)),await fs.readFile(path.resolve(__dirname,'../src',name)));
});
