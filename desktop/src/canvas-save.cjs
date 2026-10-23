'use strict';
const fs=require('node:fs/promises');
const path=require('node:path');
const {randomUUID}=require('node:crypto');
async function saveCanvasPNG(filePath,png,alive=()=>true){
  const temporary=path.join(path.dirname(filePath),'.overkill-canvas-'+randomUUID()+'.tmp');
  try {
    await fs.writeFile(temporary,png,{flag:'wx',mode:0o600});
    if(!alive())throw new Error('Canvas export was cancelled.');
    await fs.rename(temporary,filePath);
  } finally {await fs.rm(temporary,{force:true}).catch(()=>{});}
}
module.exports={saveCanvasPNG};
