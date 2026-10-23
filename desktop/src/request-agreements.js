'use strict';
(function(root) {
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
  function parsePins(text='') {
    if(typeof text!=='string'||new TextEncoder().encode(text).length>4096)throw new Error('Agreement list exceeds 4096 bytes.');
    const pins=[],seen=new Set();
    for(const entry of text.trim().split(/[\s,]+/).filter(Boolean)) {
      const [id,version,...extra]=entry.split('@'),revision=Number(version);
      if(extra.length||!uuid.test(id)||!/^\d+$/.test(version||'')||!Number.isSafeInteger(revision)||revision<1||seen.has(id))throw new Error('Choose distinct agreement UUIDs with positive revision numbers.');
      seen.add(id);pins.push({contract_id:id,revision});
    }
    if(pins.length>32)throw new Error('Choose at most 32 required agreements.');
    return pins;
  }
  const formatPins = pins => (pins||[]).map(pin=>`${pin.contract_id}@${pin.revision}`).join(', ');
  function choices(contracts,pins) {
    const saved=new Map(pins.map(pin=>[pin.contract_id,pin.revision]));
    const items=new Map(contracts.map(item=>[item.id,{id:item.id,name:item.spec?.name||item.id,current:item.revision,saved:saved.get(item.id)||0}]));
    for(const [id,revision] of saved)if(!items.has(id))items.set(id,{id,name:'Unavailable agreement',current:0,saved:revision});
    return [...items.values()];
  }
  function create({api,document:doc,session,getPins,onApply}) {
    const $=id=>doc.getElementById(id),dialog=$('request-agreements'),list=$('request-agreement-list'),error=$('request-agreement-error'),save=$('request-agreement-apply');
    let serial=0,state=null;
    const el=(tag,text)=>{const node=doc.createElement(tag);if(text!==undefined)node.textContent=text;return node;};
    function reset(){serial++;state=null;if(dialog.open)dialog.close();}
    async function open() {
      reset();const id=session()?.id;if(!id)return;
      const current={id,serial:++serial,rows:[]};state=current;error.textContent='';list.replaceChildren(el('p','Loading workspace agreements…'));save.disabled=true;dialog.showModal();
      const valid=()=>state===current&&session()?.id===id&&current.serial===serial;
      try {
        const pins=parsePins(getPins());const response=await api.contracts({sessionID:id});if(!valid())return;
        const contracts=response?.result??response;if(!Array.isArray(contracts))throw new Error('Workspace agreements could not be read.');
        list.replaceChildren();
        for(const item of choices(contracts,pins)) {
          const label=el('label',`${item.name} · ${item.id}`),select=el('select');select.dataset.contractId=item.id;
          for(const revision of [0,...new Set([item.saved,item.current].filter(Boolean))]) {
            const option=el('option',revision?`Revision ${revision}${revision===item.current?' (listed current)':' (saved pin)'}`:'Not required');option.value=String(revision);select.append(option);
          }
          select.value=String(item.saved);label.append(select);list.append(label);current.rows.push({id:item.id,select});
        }
        if(!current.rows.length)list.append(el('p','No workspace agreements yet. Create one in Contracts.'));
        save.disabled=false;
      }catch(e){if(valid()){list.replaceChildren();error.textContent=String(e.message||e);}}
    }
    save.addEventListener('click',()=>{
      if(!state||save.disabled||session()?.id!==state.id)return;
      try {const pins=state.rows.filter(row=>row.select.value!=='0').map(row=>({contract_id:row.id,revision:Number(row.select.value)}));onApply(parsePins(formatPins(pins)));reset();}catch(e){error.textContent=String(e.message||e);}
    });
    $('request-agreement-cancel').addEventListener('click',reset);dialog.addEventListener('cancel',reset);dialog.addEventListener('close',()=>{state=null;serial++;});
    return {open,reset};
  }
  const exported={parsePins,formatPins,choices,create};
  if(typeof module==='object'&&module.exports)module.exports=exported;else root.overkillRequestAgreements=exported;
})(typeof window==='undefined'?globalThis:window);
