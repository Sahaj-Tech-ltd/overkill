const assert=require('node:assert/strict');
const path=require('node:path');
const {chromium}=require(path.resolve(__dirname,'../../../desktop/node_modules/playwright'));
(async()=>{
 const browser=await chromium.launch({headless:true});
 try{
  const context=await browser.newContext();const page=await context.newPage();
  const base=process.env.OVERKILL_WEB_TEST_URL,token=process.env.OVERKILL_WEB_TEST_TOKEN,sid=process.env.OVERKILL_CANVAS_SESSION;
  assert.equal((await context.request.post(base+'/api/auth',{headers:{Authorization:'Bearer '+token}})).status(),204);
  await page.goto(base);await page.locator('#conn-dot[data-state="ok"]').waitFor();
  await page.locator('.session-row').filter({hasText:'Canvas primary'}).click();await page.locator('#canvas-toggle').click();
  await page.locator('#canvas-content[data-canvas-revision="4"]').waitFor();assert.equal(await page.locator('[data-component-id="status"]').textContent(),'Backend ready');
  await page.screenshot({path:path.resolve(__dirname,'../../../.cache/canvas-workspace-proof.png'),fullPage:true});
  const push=event=>context.request.post(base+'/api/canvas/push',{data:{sessionId:sid,jsonl:JSON.stringify(event)}});
  const injected='<img src=x onerror="window.canvasInjected=true">';
  let response=await push({dataModelUpdate:{surfaceId:'view',contents:{status:injected}}});assert.equal(response.status(),202);
  await page.locator('#canvas-content[data-canvas-revision="5"]').waitFor();assert.equal(await page.locator('[data-component-id="status"]').textContent(),injected);assert.equal(await page.locator('#canvas-content img').count(),0);assert.equal(await page.evaluate(()=>window.canvasInjected),undefined);
  response=await push({surfaceUpdate:{surfaceId:'view',components:[{id:'root',type:'Script',text:'alert(1)'}]}});assert.equal(response.status(),400);
  const committed=await (await context.request.get(base+'/api/canvas/state?sessionId='+sid)).json();assert.equal(committed.revision,5);
  const href=await page.locator('#canvas-snapshot').getAttribute('href');response=await context.request.get(base+href+'&width=640&height=360');assert.equal(response.status(),200);assert.equal(response.headers()['content-type'],'image/png');assert.equal(response.headers()['x-canvas-revision'],'5');const png=await response.body();assert.deepEqual([...png.subarray(0,8)],[137,80,78,71,13,10,26,10]);assert.ok(png.length>1000);
  await page.locator('.session-row').filter({hasText:'Canvas secondary'}).click();await page.locator('#canvas-content[data-canvas-revision="5"]').waitFor();assert.equal(await page.locator('[data-component-id="status"]').textContent(),'Other session');
  await page.locator('.session-row').filter({hasText:'Canvas primary'}).click();await page.locator('#canvas-content[data-canvas-revision="5"]').waitFor();assert.equal(await page.locator('[data-component-id="status"]').textContent(),injected);
  await page.reload();await page.locator('#conn-dot[data-state="ok"]').waitFor();await page.locator('.session-row').filter({hasText:'Canvas primary'}).click();await page.locator('#canvas-toggle').click();await page.locator('#canvas-content[data-canvas-revision="5"]').waitFor();assert.equal(await page.locator('[data-component-id="status"]').textContent(),injected);
  response=await push({deleteSurface:{surfaceId:'view'}});assert.equal(response.status(),202);await page.locator('#canvas-content[data-canvas-revision="6"]').waitFor();assert.match(await page.locator('#canvas-content').textContent(),/No canvas surfaces/);assert.equal(await page.locator('#canvas-snapshot').getAttribute('aria-disabled'),'true');
  response=await context.request.post(base+'/api/canvas/reset',{data:{sessionId:sid}});assert.equal(response.status(),200);await page.locator('#canvas-content[data-canvas-revision="7"]').waitFor();
  console.log('Actual PostgreSQL canvas, browser DOM, WebSocket updates, PNG, invalid rollback, isolation, reload, delete and reset passed');
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
