const assert = require('node:assert/strict');
const path = require('node:path');
const {chromium} = require(path.resolve(__dirname, '../../../desktop/node_modules/playwright'));
(async () => {
  const browser = await chromium.launch({headless: true});
  try {
    const context = await browser.newContext();
    const page = await context.newPage();
    const base = process.env.OVERKILL_WEB_TEST_URL;
    const token = process.env.OVERKILL_WEB_TEST_TOKEN;
    const sockets = [];
    const frames = [];
    page.on('websocket', socket => { sockets.push(socket.url()); socket.on('framereceived', frame => frames.push(frame.payload)); });
    await page.goto(base + '/?t=' + token);
    await page.locator('#token-modal:not(.hidden)').waitFor();
    assert.equal((await context.cookies()).length, 0, 'URL token must not establish a session');
    assert.equal(new URL(page.url()).searchParams.has('t'), false);
    for (const suffix of ['', '?t=' + token]) {
      const connected = await page.evaluate(suffix => new Promise(resolve => {
        const ws = new WebSocket(location.origin.replace('http', 'ws') + '/api/events' + suffix);
        ws.onopen = () => { ws.close(); resolve(true); };
        ws.onerror = () => resolve(false);
      }), suffix);
      assert.equal(connected, false, 'missing cookie/query token must not authorize browser upgrade');
    }
    await page.locator('#token-input').fill('wrong');
    await Promise.all([page.waitForResponse(response => response.url().endsWith('/api/auth') && response.status() === 401), page.locator('#token-submit').click()]);
    assert.equal((await context.cookies()).length, 0);
    await context.addCookies([{name: 'overkill-token', value: 'stale-root-token', domain: new URL(base).hostname, path: '/'}]);
    await page.locator('#token-input').fill(token);
    await page.locator('#token-submit').click();
    await page.locator('#conn-dot[data-state="ok"]').waitFor();
    assert.equal(await page.locator('#token-input').inputValue(), '');
    const cookie = (await context.cookies()).find(cookie => cookie.name === 'overkill-token');
    assert.ok(cookie && cookie.httpOnly && cookie.sameSite === 'Strict' && cookie.path === '/api');
    assert.equal((await context.cookies()).filter(cookie => cookie.name === 'overkill-token').length, 1, 'login expires stale root-path cookie');
    assert.equal(cookie.secure, false, 'local plain HTTP cookie must work');
    assert.equal(await page.evaluate(() => document.cookie.includes('overkill-token')), false);
    assert.ok(sockets.includes(base.replace('http', 'ws') + '/api/events'));
    const response = await context.request.post(base + '/api/send', {data: {sessionId: 'browser-auth-fixture', text: 'Emit the controlled authentication event.'}});
    assert.equal(response.status(), 202);
    await page.waitForFunction(() => document.getElementById('conn-dot').dataset.state === 'ok');
    for (let i = 0; !frames.some(frame => JSON.parse(frame).type === 'text_delta') && i < 100; i++) await new Promise(resolve => setTimeout(resolve, 20));
    assert.ok(frames.some(frame => JSON.parse(frame).type === 'text_delta'), 'actual authenticated browser socket receives event');
    await page.reload();
    await page.locator('#conn-dot[data-state="ok"]').waitFor();
    await page.locator('#settings-btn').click();
    await page.locator('#logout-btn').click();
    await page.locator('#token-modal:not(.hidden)').waitFor();
    assert.equal((await context.cookies()).filter(cookie => cookie.name === 'overkill-token').length, 0);
    const afterLogout = await context.request.get(base + '/api/info');
    assert.equal(afterLogout.status(), 401);
    console.log('Authenticated browser cookie setup, actual WebSocket event, reload and logout passed');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
