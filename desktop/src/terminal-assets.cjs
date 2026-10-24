'use strict';
const fs = require('node:fs/promises');
const path = require('node:path');
const types = {'terminal-frame.html':'text/html; charset=utf-8','terminal-frame.js':'text/javascript; charset=utf-8','terminal-frame.css':'text/css; charset=utf-8','xterm.js':'text/javascript; charset=utf-8','xterm-fit.js':'text/javascript; charset=utf-8','xterm.css':'text/css; charset=utf-8'};
function terminalAsset(value) {
  if (typeof value !== 'string') return null;
  for (const name of Object.keys(types)) if (value === 'overkill-terminal://view/' + name) return name;
  return null;
}
async function terminalResponse(request) {
  const name = terminalAsset(request.url);
  if (request.method !== 'GET' || !name) return new Response('Not found', {status:404});
  return new Response(await fs.readFile(path.join(__dirname,name)), {headers:{'Content-Type':types[name],'X-Content-Type-Options':'nosniff','Cache-Control':'no-store'}});
}
module.exports = {terminalAsset,terminalResponse};
