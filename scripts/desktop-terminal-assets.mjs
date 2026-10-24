import fs from 'node:fs/promises';
import path from 'node:path';
import {createHash} from 'node:crypto';
const sha=bytes=>createHash('sha256').update(bytes).digest('hex');
export async function syncDesktopTerminal(root,{check=false}={}){
 const specs=[{name:'@xterm/xterm',version:'6.0.0',files:{'lib/xterm.js':'xterm.js','css/xterm.css':'xterm.css'},license:'xterm-LICENSE.txt'}, {name:'@xterm/addon-fit',version:'0.11.0',files:{'lib/addon-fit.js':'xterm-fit.js'},license:'xterm-fit-LICENSE.txt'}];
 const notices=[];
 for(const spec of specs){
  const base=path.join(root,'desktop/node_modules',spec.name);const pkg=JSON.parse(await fs.readFile(path.join(base,'package.json')));if(pkg.version!==spec.version)throw new Error('Unexpected terminal dependency version: '+spec.name);
  for(const [source,name]of Object.entries(spec.files)){
   let bytes=await fs.readFile(path.join(base,source));if(name.endsWith('.js'))bytes=Buffer.from(bytes.toString().replace(/\n?\/\/# sourceMappingURL=.*$/m,''));
   const target=path.join(root,'desktop/src',name);
   if(check){if(!bytes.equals(await fs.readFile(target)))throw new Error('Terminal asset differs: '+name);}else await fs.writeFile(target,bytes);
  }
  const bytes=await fs.readFile(path.join(base,'LICENSE'));const relative='desktop/notices/'+spec.license;const target=path.join(root,relative);
  if(check){if(!bytes.equals(await fs.readFile(target)))throw new Error('Terminal license differs: '+spec.name);}else await fs.writeFile(target,bytes);
  notices.push({path:relative,kind:'file',bytes:bytes.length,sha256:sha(bytes),provenance:{component:spec.name+' '+spec.version,source:path.join(base,'LICENSE'),sha256:sha(bytes),bytes:bytes.length}});
 }
 const manifestPath=path.join(root,'desktop/notices/manifest.json');const manifest=JSON.parse(await fs.readFile(manifestPath));
 if(check){for(const record of notices){if(!manifest.files.some(item=>item.path===record.path&&item.sha256===record.sha256))throw new Error('Terminal notice missing from inventory: '+record.path);}}
 else {manifest.files=manifest.files.filter(item=>!notices.some(record=>record.path===item.path)).concat(notices);await fs.writeFile(manifestPath,JSON.stringify(manifest,null,2)+'\n');}
}
