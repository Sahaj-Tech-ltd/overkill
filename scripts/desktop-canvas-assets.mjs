import fs from 'node:fs/promises';
import path from 'node:path';
export async function syncDesktopCanvas(root, {check=false}={}) {
  for(const name of ['canvas.js','canvas.css']) {
    const source=await fs.readFile(path.join(root,'internal/web/static',name));
    const target=path.join(root,'desktop/src',name);
    if(check) {
      let existing;try{existing=await fs.readFile(target);}catch{throw new Error(`Missing shared desktop canvas asset ${name}; run prepare-desktop.mjs.`);}
      if(!source.equals(existing))throw new Error(`Shared desktop canvas asset ${name} differs; run prepare-desktop.mjs.`);
    } else await fs.writeFile(target,source);
  }
}
