'use strict';
window.createCanvasPanel = ({api,getSession,isReady,document,render=window.OverkillCanvas.render}) => {
  const $=id=>document.getElementById(id), panel=$('canvas-panel'),toggle=$('canvas-toggle'),picker=$('canvas-surface'),content=$('canvas-content'),status=$('canvas-status'),exportStatus=$('canvas-export-status'),error=$('canvas-error'),save=$('canvas-save');
  let generation=0,selected=null,state=null,pending=false,exporting=false;
  const flights=new Map();
  function eligible(){return isReady() && getSession() && !panel.hidden && !document.hidden;}
  function disable(){save.disabled=true;}
  function reset(){generation++;selected=getSession()?.id||null;state=null;content.replaceChildren();content.removeAttribute('data-canvas-revision');content.removeAttribute('data-surface-id');picker.replaceChildren();error.hidden=true;exportStatus.hidden=true;exportStatus.textContent='';status.textContent=selected?'Open Canvas to inspect saved surfaces.':'Select a chat to view its canvas.';disable();}
  function show(){render(content,state,picker.value||undefined);status.textContent=`Revision ${state.revision}`;save.disabled=exporting || !state.surfaces.some(surface=>surface.surfaceId===picker.value&&surface.rendering);}
  async function refresh(){
    if(!eligible())return;
    if([...flights.values()].includes(generation) || flights.size>=2){pending=true;return;}
    const token=Symbol(),id=getSession().id,epoch=generation;flights.set(token,epoch);status.textContent='Loading canvas…';
    try {
      const result=await api.canvasState({sessionID:id});
      if(epoch!==generation || getSession()?.id!==id || !isReady())return;
      const next=result.state;if(next.sessionId!==id)throw new Error('Canvas response belongs to another chat.');
      const staging=document.createElement('div');render(staging,next);
      const previous=picker.value;state=next;picker.replaceChildren();
      for(const surface of state.surfaces){const option=document.createElement('option');option.value=surface.surfaceId;option.textContent=surface.surfaceId;picker.append(option);}
      if(state.surfaces.some(surface=>surface.surfaceId===previous))picker.value=previous;
      error.hidden=true;show();
    } catch(cause){if(epoch===generation&&getSession()?.id===id){error.textContent='Could not load canvas: '+cause.message;error.hidden=false;status.textContent='Canvas unavailable';disable();}}
    finally{flights.delete(token);if(pending){pending=false;refresh();}}
  }
  toggle.addEventListener('click',()=>{panel.hidden=!panel.hidden;toggle.setAttribute('aria-expanded',String(!panel.hidden));if(!panel.hidden)refresh();});
  picker.addEventListener('change',()=>{if(state)show();});
  $('canvas-refresh').addEventListener('click',refresh);
  save.addEventListener('click',async()=>{
    if(!state || save.disabled || exporting)return;
    const id=getSession()?.id,epoch=generation,request={sessionID:id,surfaceID:picker.value,revision:state.revision};
    exporting=true;disable();exportStatus.hidden=false;exportStatus.textContent='Preparing PNG…';
    try {const result=await api.saveCanvasSnapshot(request);if(epoch!==generation||getSession()?.id!==id)return;if(result.sessionID!==id||result.surfaceID!==request.surfaceID||result.revision!==request.revision)throw new Error('Canvas export identity changed.');exportStatus.textContent=result.saved?`Saved PNG · revision ${result.revision}`:'PNG save cancelled.';}
    catch(cause){if(epoch===generation&&getSession()?.id===id){exportStatus.textContent='PNG was not saved: '+cause.message;}}
    finally{exporting=false;if(state&&isReady())save.disabled=!state.surfaces.some(surface=>surface.surfaceId===picker.value&&surface.rendering);}
  });
  const timer=setInterval(()=>{if(eligible())refresh();},2500);
  window.addEventListener('beforeunload',()=>clearInterval(timer),{once:true});
  reset();
  return Object.freeze({select(){reset();refresh();},refresh,connection(){generation++;if(!isReady()){disable();status.textContent='Backend disconnected.';}else refresh();}});
};
