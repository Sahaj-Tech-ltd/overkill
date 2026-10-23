'use strict';
(function(root) {
  const clone = value => JSON.parse(JSON.stringify(value));
  const semanticJSON = value => Array.isArray(value) ? `[${value.map(semanticJSON).join(',')}]` : value && typeof value==='object' ? `{${Object.keys(value).sort().map(key=>`${JSON.stringify(key)}:${semanticJSON(value[key])}`).join(',')}}` : JSON.stringify(value);
  function create({document: doc, api, session, onSaved}) {
    const el = (tag, text) => {const node=doc.createElement(tag);if(text!==undefined)node.textContent=text;return node;};
    const dialog=el('dialog'), form=el('form'), title=el('h2'), status=el('p'), fields={}, controls=[];
    dialog.id='contract-editor';dialog.setAttribute('aria-labelledby','contract-editor-title');title.id='contract-editor-title';
    status.id='contract-editor-status';status.setAttribute('role','alert');
    const help=el('p','Saving publishes an interface agreement and reserves its owned paths. It does not run tests or mark work complete.');help.className='muted';
    form.append(title,help);
    function field(name,label,{area=false,value='',max=4096}={}) {
      const wrapper=el('label',label), input=el(area?'textarea':'input');input.id=`contract-${name}`;input.value=value;input.maxLength=max;
      if(area)input.rows=name==='schema'?7:3;
      wrapper.append(input);fields[name]=input;controls.push(input);form.append(wrapper);return input;
    }
    field('name','Contract name',{max:256});field('endpoint','Endpoint or interface',{max:2048});field('version','Schema version',{max:128});
    field('schema','Response schema (JSON object)',{area:true,max:48000});
    field('criteria','Acceptance criteria (one per line)',{area:true,max:32000});field('paths','Owned relative paths (one per line)',{area:true,max:16000});
    const artifacts=el('div'), addArtifact=el('button','Add artifact');addArtifact.type='button';addArtifact.id='contract-add-artifact';
    const dependencies=el('div');dependencies.id='contract-dependencies';
    form.append(el('h3','Artifacts'),artifacts,addArtifact,el('h3','Pinned dependencies'),dependencies);
    const change=el('fieldset'), kind=el('select');kind.id='contract-change-kind';
    for(const value of ['','compatible','additive','breaking']){const option=el('option',value||'Choose when changing the schema or version');option.value=value;kind.append(option);}
    const changeLabel=el('label','Compatibility change');changeLabel.append(kind);change.append(changeLabel);form.append(change);
    field('change','Describe the compatibility change',{area:true,max:4096});controls.push(kind);
    const actions=el('div'), cancel=el('button','Cancel'), save=el('button','Publish contract');actions.className='dialog-actions';cancel.type='button';save.type='submit';save.id='contract-save';actions.append(cancel,save);form.append(status,actions);dialog.append(form);doc.body.append(dialog);
    let current=null, serial=0;
    const valid=state=>current===state && state.sessionID===session()?.id;
    function pending(value) {for(const control of controls)control.disabled=value;addArtifact.disabled=value;save.disabled=value;}
    function reset() {serial++;current=null;dialog.close();}
    function artifactRow(state,artifact={name:'',path:''}) {
      const row=el('div');row.className='contract-artifact';
      const name=el('input'), path=el('input'), remove=el('button','Remove artifact');remove.type='button';
      name.value=artifact.name;name.maxLength=256;path.value=artifact.path;path.maxLength=4096;
      const nameLabel=el('label','Artifact name'), pathLabel=el('label','Relative path');nameLabel.append(name);pathLabel.append(path);row.append(nameLabel,pathLabel,remove);artifacts.append(row);
      const item={original:clone(artifact),name,path,row};state.artifacts.push(item);controls.push(name,path,remove);
      remove.addEventListener('click',()=>{if(!valid(state)||state.pending)return;state.artifacts=state.artifacts.filter(other=>other!==item);row.remove();});
    }
    function open(contract,list=[]) {
      reset();controls.splice(controls.indexOf(kind)+1);const state={sessionID:session()?.id,contract:contract?clone(contract):null,artifacts:[],dependencies:[],pending:false,serial:++serial};
      if(!state.sessionID)return;current=state;pending(false);status.textContent='';
      const spec=state.contract?.spec||{};
      title.textContent=contract?`Edit ${spec.name} · revision ${contract.revision}`:'New interface contract';save.textContent=contract?'Publish next revision':'Publish contract';
      for(const [name,value] of Object.entries({name:spec.name||'',endpoint:spec.endpoint||'',version:spec.schema_version||'1',schema:JSON.stringify(spec.schema||{type:'object'},null,2),criteria:(spec.acceptance_criteria||[]).join('\n'),paths:(spec.owned_paths||[]).join('\n'),change:''}))fields[name].value=value;
      kind.value='';artifacts.replaceChildren();for(const artifact of spec.artifacts||[])artifactRow(state,artifact);
      dependencies.replaceChildren();
      const pinned=new Map((spec.dependencies||[]).map(dep=>[dep.contract_id,dep.revision]));
      const choices=new Map(list.filter(item=>item.id!==contract?.id).map(item=>[item.id,item]));
      for(const [id,revision] of pinned)if(!choices.has(id))choices.set(id,{id,revision,spec:{name:'Unavailable dependency'}});
      for(const item of choices.values()) {
        const label=el('label',`${item.spec.name} · ${item.id}`), select=el('select');select.dataset.contractId=item.id;
        const revisions=[...new Set([pinned.get(item.id),item.revision].filter(Boolean))];
        for(const revision of [0,...revisions]){const option=el('option',revision?`Pin revision ${revision}${revision===item.revision?' (listed current)':''}`:'No dependency');option.value=String(revision);select.append(option);}
        select.value=String(pinned.get(item.id)||0);label.append(select);dependencies.append(label);controls.push(select);state.dependencies.push({id:item.id,select});
      }
      if(!choices.size)dependencies.append(el('p','No other contracts in this workspace.'));
      if(!dialog.open)dialog.showModal();fields.name.focus();
    }
    addArtifact.addEventListener('click',()=>{if(current&&!current.pending)artifactRow(current);});
    cancel.addEventListener('click',reset);dialog.addEventListener('cancel',reset);dialog.addEventListener('close',()=>{current=null;});
    form.addEventListener('submit',async event=>{
      event.preventDefault();const state=current;if(!state||!valid(state)||state.pending||state.needsRefresh)return;
      let spec;
      try {
        const schema=JSON.parse(fields.schema.value);if(!schema||typeof schema!=='object'||Array.isArray(schema))throw new Error('Response schema must be a JSON object.');
        const lines=value=>value.split('\n').map(line=>line.trim()).filter(Boolean);
        spec={...(state.contract?.spec||{}),name:fields.name.value.trim(),endpoint:fields.endpoint.value.trim(),schema_version:fields.version.value.trim(),schema,acceptance_criteria:lines(fields.criteria.value),owned_paths:lines(fields.paths.value),artifacts:state.artifacts.map(item=>({...item.original,name:item.name.value.trim(),path:item.path.value.trim()})),dependencies:state.dependencies.filter(item=>Number(item.select.value)>0).map(item=>({contract_id:item.id,revision:Number(item.select.value)})),compatibility_changes:state.contract?.spec.compatibility_changes||[]};
        delete spec.workspace;
        if(!spec.name||!spec.endpoint||!spec.schema_version||!spec.acceptance_criteria.length)throw new Error('Include a name, interface, version and at least one acceptance criterion.');
        if(kind.value)spec.compatibility_changes=[...spec.compatibility_changes,{kind:kind.value,from_version:state.contract?.spec.schema_version||spec.schema_version,to_version:spec.schema_version,description:fields.change.value.trim()}];
        const interfaceChanged=state.contract&&(semanticJSON(schema)!==semanticJSON(state.contract.spec.schema)||spec.endpoint!==state.contract.spec.endpoint);
        if(interfaceChanged&&spec.schema_version===state.contract.spec.schema_version)throw new Error('Changing the interface or response schema requires a new schema version.');
        if(state.contract&&(interfaceChanged||spec.schema_version!==state.contract.spec.schema_version)&&!kind.value)throw new Error('Choose and describe compatibility when changing the interface, schema or version.');
        if(kind.value&&!fields.change.value.trim())throw new Error('Describe the compatibility change.');
      } catch(error){status.textContent=error.message;return;}
      state.pending=true;pending(true);status.textContent='Publishing agreement…';
      try {
        const input={sessionID:state.sessionID,spec};
        const response=state.contract?await api.updateContract({...input,contractID:state.contract.id,revision:state.contract.revision}):await api.createContract(input);
        if(!valid(state))return;
        const result=response?.result??response;
        if(!result?.id||result.revision!==(state.contract?state.contract.revision+1:1)||(state.contract&&result.id!==state.contract.id))throw new Error('The saved revision could not be confirmed.');
        reset();await onSaved(result);
      } catch(error) {
        if(!valid(state))return;
        status.textContent=`${error.message} Your draft is retained. Close and refresh contracts before submitting again; the save may have committed.`;
        // No automatic retry: create has no idempotency key. Preserve the draft
        // for inspection, but require a fresh list/revision before resubmission.
        state.needsRefresh=true;save.disabled=true;
      } finally {if(valid(state)){state.pending=false;for(const control of controls)control.disabled=false;addArtifact.disabled=false;}}
    });
    return {open,reset};
  }
  const exported={create};if(typeof module==='object'&&module.exports)module.exports=exported;else root.overkillContractEditor=exported;
})(typeof window==='undefined'?globalThis:window);
