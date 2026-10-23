import type {BackendClient} from '../backend/client.ts';
import {canonicalTheme,themeCatalog} from './catalog.ts';
export interface ThemeSnapshot {themeName:string;error:string|null;pending:boolean}
export class ThemeStore {
 private state:ThemeSnapshot;private listeners=new Set<()=>void>();private backend:Pick<BackendClient,'call'>|null=null;private controller:AbortController|null=null;private epoch=0;private revision=0;private errorRevision=0;private owners=0;private queue:Promise<void>=Promise.resolve();private envOverride=false;
 constructor(env=process.env['OVERKILL_THEME']) {
  let themeName='dark',error:string|null=null;
  if(env){try{themeName=canonicalTheme(env);this.envOverride=true;}catch(reason){error=(reason as Error).message;}}
  this.state={themeName,error,pending:false};
 }
 getSnapshot=()=>this.state;
 subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
 private update(patch:Partial<ThemeSnapshot>){this.state={...this.state,...patch};this.listeners.forEach(listener=>listener());}
 connect(backend:Pick<BackendClient,'call'>){
  if(this.backend===backend){this.owners++;return()=>this.disconnect(backend);}
  this.controller?.abort();this.backend=backend;this.controller=new AbortController();this.owners=1;const epoch=++this.epoch,revision=this.revision,signal=this.controller.signal;
  this.update({pending:true});
  void backend.call<{theme:string}>('config.theme',undefined,{signal}).then(result=>{
   if(epoch!==this.epoch||revision!==this.revision)return;
   const themeName=canonicalTheme(result.theme===''?'dark':result.theme);
   this.update({...(this.envOverride?{}:{themeName}),pending:false,error:this.state.error});
  }).catch(reason=>{if(epoch===this.epoch&&revision===this.revision)this.update({pending:false,error:'Theme load failed: '+(reason as Error).message});});
  return()=>this.disconnect(backend);
 }
 private disconnect(backend:Pick<BackendClient,'call'>){if(this.backend!==backend||--this.owners>0)return;this.controller?.abort();this.controller=null;this.backend=null;this.epoch++;this.update({pending:false});}
 setTheme=(name:string):Promise<void>=>{
  let canonical:string;
  try{canonical=canonicalTheme(name);}catch(reason){this.errorRevision++;this.update({error:(reason as Error).message});return Promise.reject(reason);}
  const backend=this.backend,epoch=this.epoch,revision=++this.revision,errorRevision=++this.errorRevision,signal=this.controller?.signal;
  if(!backend){const error=new Error('Theme backend is not connected; selection was not saved.');this.update({error:error.message});return Promise.reject(error);}
  this.update({pending:true,error:null});
  const operation=this.queue.catch(()=>{}).then(async()=>{
   if(epoch!==this.epoch)throw new Error('Theme selection was cancelled after disconnect.');
   const result=await backend.call<{theme:string}>('config.theme',{theme:canonical},{signal});
   if(canonicalTheme(result.theme)!==canonical)throw new Error('Backend did not confirm the selected theme.');
   if(epoch===this.epoch&&revision===this.revision){this.envOverride=false;this.update({themeName:canonical,pending:false,error:errorRevision===this.errorRevision?null:this.state.error});}
  }).catch(reason=>{if(epoch===this.epoch&&revision===this.revision)this.update({pending:false,error:errorRevision===this.errorRevision?'Theme save failed: '+(reason as Error).message:this.state.error});throw reason;});
  this.queue=operation.catch(()=>{});return operation;
 };
 get availableThemes(){return Object.values(themeCatalog).map(theme=>({name:theme.name,label:theme.label}));}
}
export const sharedThemeStore=new ThemeStore();
