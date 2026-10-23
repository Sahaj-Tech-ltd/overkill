import type {Theme} from './definitions.ts';
import {cyberpunk,ocean} from './definitions.ts';
import {catppuccin,themes} from './all-themes.ts';

const light:Theme={name:'light',label:'Light',background:'#ffffff',text:'#24292f',accent:'#0550ae',muted:'#57606a',error:'#cf222e',success:'#116329',warning:'#7d4e00',border:'#d0d7de',inputBg:'#f6f8fa',highlight:'#8250df',syntax:{keyword:'#cf222e',string:'#0a3069',comment:'#57606a',number:'#0550ae',type:'#953800',function:'#8250df',operator:'#cf222e',text:'#24292f'}};
// Canonical identifiers match the fixed backend config.theme contract.
export const themeCatalog:Readonly<Record<string,Theme>>={...themes,dark:{...catppuccin,name:'dark',label:'Dark (Catppuccin)'},light,cyberpunk,ocean};
const aliases:Readonly<Record<string,string>>={'catppuccin-mocha':'catppuccin','tokyo-night':'tokyonight','rose-pine':'rosepine'};
export function canonicalTheme(name:string):string {
 const canonical=aliases[name]??name;
 if(!Object.hasOwn(themeCatalog,canonical))throw new Error(`Unknown terminal theme: ${JSON.stringify(name)}. Choose a name from the available terminal themes.`);
 return canonical;
}
