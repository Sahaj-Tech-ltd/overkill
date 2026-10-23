#!/usr/bin/env python3
"""Generate an offline, read-only view of the research index; never infer parity."""
import argparse
import base64
import gzip
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import tempfile
from urllib.parse import quote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
REPORTS = ROOT / 'docs/harness-research'


def script_json(value):
    # A JSON script element still follows HTML raw-text parsing.
    return json.dumps(value, ensure_ascii=True, separators=(',', ':')).replace('<', '\\u003c').replace('>', '\\u003e').replace('&', '\\u0026')


def safe_http(value):
    if not isinstance(value, str) or any(ord(c) < 32 for c in value):
        return None
    parsed = urlsplit(value)
    return value if parsed.scheme in ('https', 'http') and parsed.netloc else None


def local_link(value, root, reports):
    if not isinstance(value, str) or not value or '\\' in value or any(ord(c) < 32 for c in value):
        return None
    p = Path(value)
    if not p.is_absolute():
        p = root / p
    p = p.resolve()
    if not p.is_relative_to(root.resolve()):
        return None
    return quote(os.path.relpath(p, reports), safe='/')


def evidence_links(record, root, reports, slug):
    links, seen = [], set()
    def walk(value):
        if isinstance(value, list):
            for v in value:
                walk(v)
        elif isinstance(value, dict):
            if any(k in value for k in ('path', 'local_path', 'url', 'source_url')):
                item = {k: value[k] for k in ('kind', 'path', 'local_path', 'line', 'end_line', 'role', 'sha256') if k in value}
                urls = [u for k in ('url', 'source_url') if (u := safe_http(value.get(k)))]
                path = value.get('local_path', value.get('path'))
                if isinstance(path, str):
                    candidate = root / path
                    if not Path(path).is_absolute() and not candidate.exists():
                        candidate = root / 'research/harnesses' / slug / 'source' / path
                    href = local_link(str(candidate), root, reports)
                    if href and candidate.exists():
                        item['local_href'] = href
                if urls:
                    item['urls'] = urls
                if item and ('local_href' in item or urls or path):
                    key = json.dumps(item, sort_keys=True)
                    if key not in seen:
                        seen.add(key)
                        links.append(item)
            for k, v in value.items():
                # Historical copies have their own complete raw-report link.
                if k not in ('excerpt', 'previous_snapshots', 'history', 'historical_mapping', 'upstream_record'):
                    walk(v)
    walk(record)
    return links


def build_data(reports=REPORTS, root=ROOT):
    raw = (reports / 'catalogue.json').read_bytes()
    catalogue = json.loads(raw)
    if catalogue.get('schema_version') != 1 or catalogue.get('validation', {}).get('errors'):
        raise ValueError('Catalogue schema unsupported or catalogue contains validation errors')
    harnesses = catalogue['harnesses']
    slugs = {h['slug'] for h in harnesses}
    if len(slugs) != len(harnesses):
        raise ValueError('Duplicate harness')
    cache = {}
    def report(name):
        if not isinstance(name, str) or Path(name).name != name or not name.endswith('.json'):
            raise ValueError(f'Unsafe report name: {name!r}')
        if name not in cache:
            source = json.loads((reports / name).read_text())
            index = {}
            for field in ('features', 'records', 'capabilities'):
                for record in source.get(field, []):
                    rid = record.get('feature_id', record.get('id'))
                    if rid in index:
                        raise ValueError(f'Duplicate report ID: {name}/{rid}')
                    index[rid] = record
            cache[name] = index
        return cache[name]
    rows, identities = [], set()
    for layer, key in (('original', 'entries'), ('normalized', 'normalized_capabilities')):
        for entry in catalogue[key]:
            slug, rid = entry['harness'], entry['id']
            identity = (layer, slug, entry.get('report') if layer == 'original' else entry.get('parity_report'), rid)
            if slug not in slugs or identity in identities or not isinstance(rid, str) or not rid:
                raise ValueError(f'Invalid or duplicate indexed identity: {identity}')
            identities.add(identity)
            filename = entry.get('report') if layer == 'original' else entry['parity_report']
            source = report(filename).get(rid)
            if source is None:
                raise ValueError(f'Missing report record: {filename}/{rid}')
            parity_file = entry.get('parity_report')
            parity = report(parity_file).get(rid, {}) if parity_file else {}
            assessment = parity.get('overkill', parity)
            summaries = {}
            for prefix, obj in (('record', source), ('assessment', assessment), ('upstream', source.get('upstream', {}))):
                for field in ('description', 'contract', 'reason', 'notes', 'acceptance_scope', 'scope', 'boundary', 'availability_boundary', 'runtime_validation'):
                    if isinstance(obj.get(field), str):
                        summaries[prefix + '.' + field] = obj[field]
            row = dict(entry)
            row.update(layer=layer, upstream_status=entry.get('upstream_status', entry.get('source_status', 'unknown')),
                       overkill_status=entry.get('overkill_status', 'unassessed'), report=filename,
                       summaries=summaries, evidence=evidence_links(source, root, reports, slug))
            if parity and parity is not source:
                row['evidence'] += evidence_links(parity, root, reports, slug)
            rows.append(row)
    validation = catalogue['validation']
    counts = Counter(r['layer'] for r in rows)
    if counts['original'] != validation['records'] or counts['normalized'] != validation['additional_normalized_capabilities']:
        raise ValueError('Catalogue validation totals disagree with record arrays')
    for h in harnesses:
        if sum(r['layer'] == 'original' and r['harness'] == h['slug'] for r in rows) != h['records']:
            raise ValueError(f'Harness count mismatch: {h["slug"]}')
    return dict(rows=rows, harnesses=harnesses, method=catalogue['method'], limitation=catalogue['limitation'],
                normalization_scope=catalogue['normalization_scope'], normalization_reports=catalogue['complete_normalization_reports'],
                catalogue_sha256=hashlib.sha256(raw).hexdigest(), totals=dict(counts),
                changed_parity_source_paths=validation.get('changed_parity_source_paths', []))


TEMPLATE = r'''<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'none'; img-src 'none'; base-uri 'none'; form-action 'none'">
<title>Overkill · Harness research explorer</title>
<style>
:root{color-scheme:dark;background:#10141d;color:#e0e7f3;font:15px/1.5 system-ui,sans-serif}*{box-sizing:border-box}body{margin:0}header{padding:28px 32px;background:#161e2c;border-bottom:1px solid #34425a}h1{font-size:26px;margin:0 0 8px}h2{font-size:19px}p{max-width:1100px;margin:8px 0}a{color:#9dc8ff;overflow-wrap:anywhere}button,input,select{font:inherit;color:inherit;background:#1c2739;border:1px solid #50627e;border-radius:6px;padding:8px}button{cursor:pointer}button:hover,button:focus{background:#31466a}label{display:flex;flex-direction:column;gap:4px;font-size:13px;color:#bdcbe1}.filters{display:grid;grid-template-columns:2fr repeat(5,1fr);gap:12px;padding:20px 32px}.summary{padding:0 32px 16px;color:#aec4e7}.layout{display:grid;grid-template-columns:minmax(430px,1fr) minmax(350px,1fr);gap:20px;padding:0 32px 32px}table{width:100%;border-collapse:collapse;table-layout:fixed}th{text-align:left;color:#a8bad4;font-weight:500}td,th{padding:10px 8px;border-bottom:1px solid #2a364a;vertical-align:top;overflow-wrap:anywhere}td:first-child{width:50%}td button{text-align:left;width:100%;background:transparent;border:0;padding:0;color:#c9dfff}td small{display:block;color:#98aac5}tr.selected{background:#25364e}.pager{display:flex;gap:12px;align-items:center;margin:16px 0}.detail{background:#17202e;padding:22px;border:1px solid #34425a;border-radius:10px;overflow-wrap:anywhere;align-self:start;position:sticky;top:12px;max-height:85vh;overflow:auto}.tag{display:inline-block;background:#263650;padding:3px 8px;border-radius:5px;margin:3px 5px 3px 0;font-size:13px}.muted{color:#acbbcf}.evidence{border-top:1px solid #35435a;margin-top:12px;padding-top:10px;font-size:13px}pre{white-space:pre-wrap;font-size:12px}details{margin:12px 0}footer{padding:20px 32px;border-top:1px solid #34425a;font-size:12px;color:#9cabbe}@media(max-width:1100px){.filters{grid-template-columns:repeat(3,1fr)}.layout{grid-template-columns:1fr}.detail{position:static;max-height:none}}@media(max-width:650px){header,.filters,.layout,.summary,footer{padding-left:14px;padding-right:14px}.filters{grid-template-columns:1fr 1fr}.layout{display:block}.detail{margin-top:20px}}
</style>
<header><h1>Harness research explorer</h1><p id="totals"></p><p>Original discovery records and overlapping normalized units are separate inventories. Neither total is a count of unique features or a parity score.</p><details><summary>Coverage, evidence and availability limits</summary><p id="method"></p><p id="limits"></p><p id="scope"></p><p>Statuses are preserved from the catalogue. “Unassessed” is not “missing”; “unknown” is insufficient evidence. Static source observations and historical mappings do not certify current runtime parity. Open any raw report to see full excerpts and scope. Links to local evidence require this repository; official links require internet.</p><div id="harness-notes"></div><p id="stale"></p></details></header>
<div class="filters"><label>Search IDs, names, contracts, evidence paths<input id="query" type="search" placeholder="e.g. worktree, MCP, retry"></label><label>Inventory<select id="layer"><option value="original">Original records</option><option value="normalized">Normalized units (overlap)</option><option value="">Both (overlap)</option></select></label><label>Harness<select id="harness"></select></label><label>Overkill parity<select id="parity"></select></label><label>Upstream status<select id="upstream"></select></label><label>Evidence status<select id="evidence-status"></select></label><label>Category<select id="category"></select></label><label>Sort<select id="sort"><option value="name">Name A–Z</option><option value="id">ID A–Z</option><option value="harness">Harness</option><option value="overkill_status">Overkill parity</option><option value="upstream_status">Upstream status</option></select></label></div>
<div id="summary" class="summary" role="status" aria-live="polite"></div><main class="layout"><section><table aria-label="Matching research records"><thead><tr><th style="width:52%">Record</th><th>Harness / inventory</th><th>Overkill / upstream</th></tr></thead><tbody id="rows"></tbody></table><div class="pager"><button id="prev">Previous</button><span id="page"></span><button id="next">Next</button></div></section><section id="detail" class="detail" aria-label="Selected record"><p>Select a record to inspect its scope and evidence.</p></section></main><footer id="provenance"></footer>
<script id="research-data" type="application/octet-stream">__DATA__</script>
<script>
'use strict';
async function boot(){
const bytes=Uint8Array.from(atob(document.getElementById('research-data').textContent),c=>c.charCodeAt(0));
const data=await new Response(new Blob([bytes]).stream().pipeThrough(new DecompressionStream('gzip'))).json(), $=id=>document.getElementById(id), size=50;
let filtered=[],page=0,selected=null;
const text=(tag,value,parent,cls)=>{const n=document.createElement(tag);n.textContent=String(value??'');if(cls)n.className=cls;if(parent)parent.append(n);return n};
function link(label,href,parent){if(typeof href!=='string')return;let parsed;try{parsed=new URL(href,location.href)}catch{return}if(!['http:','https:','file:'].includes(parsed.protocol))return;if(!/^https?:\/\//.test(href)&&(!href.startsWith('../')&&!/^[a-z0-9_-]+\.json$/.test(href)))return;const n=text('a',label,parent);n.href=href;n.target='_blank';n.rel='noopener noreferrer';return n}
function choices(id,values){const s=$(id);const first=text('option','All',s);first.value='';for(const v of [...new Set(values)].sort()){const n=text('option',v,s);n.value=v}}
$('totals').textContent=`${data.totals.original.toLocaleString()} original discovery records · ${data.totals.normalized.toLocaleString()} overlapping normalized units · ${data.harnesses.length} harnesses`;
$('method').textContent=data.method;$('limits').textContent=data.limitation;$('scope').textContent=data.normalization_scope;
for(const h of data.harnesses){const p=text('p','',$('harness-notes'));text('strong',h.name+': ',p);text('span',`${h.records} original records. DeepWiki: ${h.deepwiki_status}. ${h.source.availability||''} Source: ${h.source.commit||'documentation snapshot'}. `,p);link('Official source',h.source.url,p)}
$('stale').textContent=`${data.changed_parity_source_paths.length} Overkill evidence paths have changed since recorded mappings. Source-changed flags require review; labels remain historical.`;
$('provenance').textContent='Offline generated index · catalogue SHA-256 '+data.catalogue_sha256+' · Read-only; no source code is executed and no network requests are made by this page.';
choices('harness',data.harnesses.map(h=>h.slug));choices('parity',data.rows.map(r=>r.overkill_status));choices('upstream',data.rows.map(r=>r.upstream_status));choices('evidence-status',data.rows.map(r=>r.parity_evidence_status||'unassessed'));choices('category',data.rows.map(r=>r.category||'unknown'));
const search=data.rows.map(r=>JSON.stringify(r).toLocaleLowerCase());
function filter(){const q=$('query').value.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);filtered=data.rows.filter((r,i)=>(!$('layer').value||r.layer===$('layer').value)&&(!$('harness').value||r.harness===$('harness').value)&&(!$('parity').value||r.overkill_status===$('parity').value)&&(!$('upstream').value||r.upstream_status===$('upstream').value)&&(!$('evidence-status').value||(r.parity_evidence_status||'unassessed')===$('evidence-status').value)&&(!$('category').value||(r.category||'unknown')===$('category').value)&&q.every(t=>search[i].includes(t)));const k=$('sort').value;filtered.sort((a,b)=>String(a[k]||'').localeCompare(String(b[k]||''))||a.id.localeCompare(b.id)||a.layer.localeCompare(b.layer));page=0;if(selected&&!filtered.includes(selected)){selected=null;$('detail').replaceChildren();text('p','Select a record to inspect its scope and evidence.',$('detail'))}render()}
function render(){const originals=filtered.filter(r=>r.layer==='original').length;$('summary').textContent=`Matches: ${originals.toLocaleString()} originals · ${(filtered.length-originals).toLocaleString()} normalized units (overlap). Select a title for evidence.`;$('rows').replaceChildren();for(const r of filtered.slice(page*size,(page+1)*size)){const tr=document.createElement('tr');if(selected===r)tr.className='selected';const name=text('td','',tr),button=text('button',r.name||r.id,name);button.onclick=()=>{selected=r;detail(r);render()};text('small',r.id,name);const h=text('td',r.harness,tr);text('small',r.layer,h);const s=text('td',r.overkill_status,tr);text('small',r.upstream_status,s);$('rows').append(tr)}$('prev').disabled=page===0;$('next').disabled=(page+1)*size>=filtered.length;$('page').textContent=filtered.length?`${page*size+1}–${Math.min((page+1)*size,filtered.length)} of ${filtered.length}`:'No matching records'}
function detail(r){const box=$('detail');box.replaceChildren();text('h2',r.name||r.id,box);text('p',r.id,box,'muted');for(const t of [r.harness,r.layer,r.category,'Overkill: '+r.overkill_status,'Upstream: '+r.upstream_status,r.parity_evidence_status||'unassessed'])text('span',t,box,'tag');const p=text('p','',box);link('Raw record report',r.report,p);if(r.parity_report&&r.parity_report!==r.report){text('span',' · ',p);link('Raw parity report',r.parity_report,p)}for(const [k,v] of Object.entries(r.summaries)){text('h3',k.replaceAll('.',' · '),box);text('p',v,box)}if(r.source_record_ids){text('h3','Original record references',box);text('pre',r.source_record_ids.join('\n'),box)}if(r.normalization){text('h3','Normalization disposition and IDs',box);text('pre',JSON.stringify(r.normalization,null,2),box)}text('h3',`Evidence references (${r.evidence.length})`,box);if(!r.evidence.length)text('p','No direct evidence references in this indexed record. Inspect its raw report and referenced normalization records; no evidence is inferred here.',box);for(const e of r.evidence){const node=text('div','',box,'evidence');text('p',[e.kind,e.role].filter(Boolean).join(' · '),node);if(e.path||e.local_path)text('p',(e.path||e.local_path)+(e.line?':'+e.line:'')+(e.end_line?'–'+e.end_line:''),node);if(e.local_href)link('Open local evidence',e.local_href,node);for(const u of e.urls||[]){text('span',' · ',node);link(u,u,node)}if(e.sha256)text('p','SHA-256 '+e.sha256,node)}}
let timer;for(const id of ['query','layer','harness','parity','upstream','evidence-status','category','sort'])$(id).addEventListener(id==='query'?'input':'change',()=>{clearTimeout(timer);timer=setTimeout(filter,id==='query'?120:0)});$('prev').onclick=()=>{page--;render()};$('next').onclick=()=>{page++;render()};filter();
}
boot().catch(error=>{document.getElementById('summary').textContent='Unable to load the offline index. Use a modern browser with DecompressionStream support. '+error.message});
</script></html>'''


def generate(reports=REPORTS, root=ROOT, output=None):
    data = build_data(reports, root)
    payload = base64.b64encode(gzip.compress(script_json(data).encode(), mtime=0)).decode('ascii')
    html = TEMPLATE.replace('__DATA__', payload)
    output = output or reports / 'explorer.html'
    output.parent.mkdir(parents=True, exist_ok=True)
    # Validation and serialization finish before touching the published artifact.
    fd, name = tempfile.mkstemp(prefix='.explorer-', dir=output.parent)
    try:
        with os.fdopen(fd, 'w') as f:
            f.write(html)
            f.flush()
            os.fsync(f.fileno())
        os.replace(name, output)
    finally:
        if os.path.exists(name):
            os.unlink(name)
    return {'originals': data['totals']['original'], 'normalized_units': data['totals']['normalized'],
            'represented_records': len(data['rows']), 'harnesses': len(data['harnesses']),
            'catalogue_sha256': data['catalogue_sha256'], 'bytes': output.stat().st_size}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--reports', type=Path, default=REPORTS)
    parser.add_argument('--root', type=Path, default=ROOT)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    print(json.dumps(generate(args.reports.resolve(), args.root.resolve(), args.output), indent=2))
