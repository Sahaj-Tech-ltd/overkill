(function(root) {
  'use strict';
  function create({api, document, getSession, canOperate, onBusyChange=()=>{}}) {
    const panel=document.getElementById('project-skills'), list=document.getElementById('skill-drafts'),
      status=document.getElementById('skill-status'), generate=document.getElementById('skill-generate'), refresh=document.getElementById('skill-refresh');
    let generation=0, busy=false;
    function reset(){generation++;list.replaceChildren();status.textContent='';panel.open=false;}
    function controls(){generate.disabled=refresh.disabled=busy||!getSession()||!canOperate();}
    async function act(operation,id,approve) {
      const session=getSession()?.id, epoch=generation;
      if(!session||busy||!canOperate())return;
      busy=true;onBusyChange(true);controls();status.textContent=operation==='generate'?'Generating proposals using this chat’s provider…':'Saving project skill change…';
      try {
        const input={session_id:session,operation};if(id)input.id=id;if(typeof approve==='boolean')input.approve=approve;
        const result=await api.evolution(input);
        if(epoch!==generation||getSession()?.id!==session)return;
        if(operation==='generate')status.textContent=`${result.recordsLoaded||0} recorded turns · ${result.candidateDrafts||0} new proposals · ${result.quarantinedDrafts||0} quarantined · ${result.generationErrors||0} generation errors`;
        else status.textContent='Project skill change saved.';
        await load(session,epoch);
      } catch(error) {if(epoch===generation&&getSession()?.id===session)status.textContent=String(error.message||'Could not update project skills.');}
      finally {busy=false;onBusyChange(false);controls();}
    }
    async function load(session,epoch){
      const drafts=await api.evolution({session_id:session,operation:'list'});
      if(epoch!==generation||getSession()?.id!==session)return;
      list.replaceChildren();
      for(const draft of drafts||[]){
        const article=document.createElement('article');article.dataset.draftId=draft.id;article.dataset.status=draft.status;
        const heading=document.createElement('h3');heading.textContent=`${draft.targetSkillName} · ${draft.status}`;
        const operation=document.createElement('p');
        const labels={create:'Create a new project skill',replace:'Replace the skill’s complete content',append:'Append this content to the existing skill'};
        operation.textContent=labels[draft.changeKind]||'Unsupported change';
        if(draft.changeKind==='replace')operation.textContent+=' · Applying overwrites existing guidance for this skill.';
        const body=document.createElement('pre');body.textContent=draft.bodyOrPatch;article.append(heading,operation,body);
        if(draft.status==='accepted'&&draft.baseHash){const base=document.createElement('p');base.className='muted';base.textContent='Reviewed base: '+draft.baseHash+'. Applying stops if the existing skill changes.';article.append(base);}
        function button(label,operation,approve){const b=document.createElement('button');b.type='button';b.textContent=label;b.addEventListener('click',()=>void act(operation,draft.id,approve));article.append(b);}
        if(draft.status==='candidate'){button('Approve proposal','review',true);button('Reject proposal','review',false);}
        if(draft.status==='accepted')button('Apply reviewed skill','apply');
        if(draft.status==='applied')button('Roll back skill','rollback');
        if(['applying','rolling_back'].includes(draft.status)) {
          const note=document.createElement('p');note.textContent='A prior change was interrupted. Inspect the current file before retrying.';article.append(note);
          button(draft.status==='applying'?'Finish reviewed apply':'Finish rollback',draft.status==='applying'?'apply':'rollback');
        }
        list.append(article);
      }
      if(!drafts?.length){const p=document.createElement('p');p.textContent='No proposals yet. Complete a few related tasks, then generate drafts.';list.append(p);}
    }
    async function sync(){const session=getSession()?.id,epoch=generation;controls();if(!session||busy||!canOperate())return;busy=true;onBusyChange(true);controls();try{await load(session,epoch);}catch(e){if(epoch===generation)status.textContent=String(e.message);}finally{busy=false;onBusyChange(false);controls();}}
    panel.addEventListener('toggle',()=>{if(panel.open)void sync();});
    refresh.addEventListener('click',()=>void sync());generate.addEventListener('click',()=>void act('generate'));
    return {reset,controls,sync};
  }
  if(typeof module==='object'&&module.exports)module.exports={create};else root.overkillEvolution={create};
})(typeof window==='object'?window:globalThis);
