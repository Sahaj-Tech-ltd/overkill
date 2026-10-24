'use strict';
(() => {
 let token=null,sequence=Promise.resolve(),controlRevision=null,inputEpoch=null;
 const term=new Terminal({fontSize:12,fontFamily:'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',scrollback:2000,disableStdin:true,convertEol:false,windowOptions:{},linkHandler:{activate(){}},theme:{background:'#141517',foreground:'#d8dfdb',cursor:'#b7d2bf'}});
 const fit=new FitAddon.FitAddon();term.loadAddon(fit);term.open(document.getElementById('terminal'));
 // Terminal output must not read/write the clipboard or activate links.
 term.parser.registerOscHandler(52,()=>true);term.parser.registerOscHandler(8,()=>true);
 const send=(type,data={})=>{if(token)parent.postMessage({type,token,controlRevision,inputEpoch,...data},'*');};
 term.onData(data=>{if(!term.options.disableStdin)send('input',{data});});term.onBinary(data=>{if(!term.options.disableStdin)send('binary',{bytes:btoa(data)});});
 let lastSize='';function resize(){try{fit.fit();const key=term.rows+':'+term.cols;if(key!==lastSize&&term.rows>0&&term.cols>0){lastSize=key;send('resize',{rows:term.rows,cols:term.cols});}}catch{}}
 new ResizeObserver(resize).observe(document.getElementById('terminal'));
 window.addEventListener('message',event=>{
  if(event.source!==parent||!event.data||typeof event.data!=='object')return;
  const value=event.data;
  if(value.type==='init'&&typeof value.token==='string'){
   token=value.token;controlRevision=null;inputEpoch=null;term.options.disableStdin=true;const current=token;sequence=sequence.catch(()=>{}).then(()=>new Promise(resolve=>term.write('',()=>{if(current===token){term.reset();lastSize='';resize();send('ready');}resolve();})));return;
  }
  if(value.token!==token||!token)return;
  if(value.type==='output'&&typeof value.bytes==='string'){
   const bytes=Uint8Array.from(atob(value.bytes),char=>char.charCodeAt(0));const current=token;
   sequence=sequence.then(()=>new Promise(resolve=>{if(current!==token){resolve();return;}term.write(bytes,()=>{if(current===token)send('written',{serial:value.serial});resolve();});}));
  }else if(value.type==='control'){controlRevision=Number.isSafeInteger(value.controlRevision)&&value.controlRevision>0?value.controlRevision:null;inputEpoch=Number.isSafeInteger(value.inputEpoch)?value.inputEpoch:null;const wasDisabled=term.options.disableStdin;term.options.disableStdin=value.enabled!==true;if(value.enabled===true){if(wasDisabled){lastSize='';resize();}if(value.focus===true)term.focus();}}
  else if(value.type==='reset'){const current=token;sequence=sequence.then(()=>{if(current===token){term.reset();resize();}});}
 });
})();
