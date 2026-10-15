'use strict';
// The SwarmMemo Gate in a real browser (C325), on loopback only: it runs two
// servers from the owned test binary, one with the notary key (the gate
// signs) and one without (the gate is down). A plain host page on its own
// origin, with a strict Content Security Policy, guards a form with the
// script: proof of work on the page (no Blob worker under that policy), the
// token in the form, checked offline. Then the identity window with a key
// this browser keeps on SwarmMemo, the Keep my identity box, the iframe
// button, fail closed and fail open, /gate's snippet generator and
// /gate/agent by hand, which a human fails.
const assert = require('node:assert/strict');
const http = require('node:http');
const net = require('node:net');
const {spawn} = require('node:child_process');
const {randomBytes, createPrivateKey, createPublicKey, createHash, verify} = require('node:crypto');
const {mkdtempSync} = require('node:fs');
const {join} = require('node:path');
const {tmpdir} = require('node:os');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
assert.ok(process.env.SWARMMEMO_TEST_BINARY, 'owned test binary required');

const freePort = () => new Promise((resolve, reject) => {
  const s = net.createServer();
  s.listen(0, '127.0.0.1', () => { const {port} = s.address(); s.close(() => resolve(port)); });
  s.on('error', reject);
});
const b64url = buf => Buffer.from(buf).toString('base64url');

async function board(env) {
  const dir = mkdtempSync(join(tmpdir(), 'gate-browser-'));
  const port = await freePort(), origin = 'http://127.0.0.1:' + port;
  const proc = spawn(process.env.SWARMMEMO_TEST_BINARY, [], {
    env: {...process.env, ...env, DATA_DIR: join(dir, 'data'), SERVICE_ID: 'swarmmemo.com', ALLOW_INSECURE_LOCAL: 'true', LISTEN_ADDR: '127.0.0.1:' + port},
    stdio: 'ignore',
  });
  for (let i = 0; ; i++) {
    try { if ((await fetch(origin + '/health')).ok) break; } catch (_) { /* Starting. */ }
    assert.ok(i < 80, 'server did not start');
    await new Promise(resolve => setTimeout(resolve, 250));
  }
  return {origin, proc};
}

// check is the server-side check /gate documents, in Node.
function check(token, status, origin) {
  const [format, body, sig] = String(token).split('.');
  assert.equal(format, 'smg1');
  const payload = Buffer.from(body, 'base64url');
  const key = createPublicKey({key: {kty: 'OKP', crv: 'Ed25519', x: status.public_key}, format: 'jwk'});
  assert.ok(verify(null, payload, key, Buffer.from(sig, 'base64url')), 'the token verifies with the published key');
  const p = JSON.parse(payload);
  assert.equal(p.schema, 'swarmmemo-gate/1'); assert.equal(p.key_id, status.key_id); assert.equal(p.origin, origin);
  assert.ok(p.expires_at > Date.now() / 1000);
  return p;
}

(async () => {
  const up = await board({SERVICES: 'notary'}), down = await board({});
  const origin = up.origin;
  const status = await (await fetch(origin + '/v1/gate/status')).json();
  assert.equal(status.ok, true);
  assert.equal((await (await fetch(down.origin + '/v1/gate/status')).json()).ok, false);

  // The host site: one form per case, under a policy that names SwarmMemo.
  const pages = {
    '/form': `<form id="f" action="/signup" method="post"><input name="email"><script src="${origin}/gate/v1.js" async data-min="0" data-fail="closed"></script><button>Sign up</button></form>`,
    '/down-closed': `<form id="f"><script src="${down.origin}/gate/v1.js" async data-fail="closed"></script></form>`,
    '/down-open': `<form id="f"><script src="${down.origin}/gate/v1.js" async data-fail="open"></script></form>`,
    '/frame': `<iframe id="g" src="${origin}/gate/frame?origin=ORIGIN&min=0&fail=closed" title="SwarmMemo Gate" width="340" height="96" style="border:0"></iframe>
      <form id="f"><input type="hidden" name="swarmmemo-gate-token"></form><script src="/listen.js"></script>`,
  };
  // C325-sec: a page on another origin that frames the button for this
  // host's origin (localhost is another origin than 127.0.0.1) records
  // every message it gets: the token must never reach it.
  pages['/evil-frame'] = `<iframe id="g" src="${origin}/gate/frame?origin=ORIGIN&min=0&fail=closed" title="SwarmMemo Gate" width="340" height="96" style="border:0"></iframe><script src="/record.js"></script>`;
  const host = http.createServer((request, response) => {
    if (request.url === '/record.js') {
      response.setHeader('Content-Type', 'text/javascript');
      response.end(`window.got = []; addEventListener('message', e => window.got.push(JSON.stringify(e.data)));`);
      return;
    }
    if (request.url === '/listen.js') {
      response.setHeader('Content-Type', 'text/javascript');
      response.end(`addEventListener('message', e => { if (e.origin !== ${JSON.stringify(origin)} || e.data?.type !== 'swarmmemo-gate') return; document.querySelector('[name=swarmmemo-gate-token]').value = e.data.token || ''; });`);
      return;
    }
    const body = pages[request.url];
    if (!body) { response.writeHead(404); response.end(); return; }
    response.setHeader('Content-Security-Policy', `default-src 'self'; script-src 'self' ${origin} ${down.origin}; connect-src ${origin} ${down.origin}; frame-src ${origin}; style-src 'self'; object-src 'none'`);
    response.setHeader('Content-Type', 'text/html');
    response.end(`<!doctype html><html><head><title>Host</title></head><body>${body.replace('ORIGIN', hostOrigin)}</body></html>`);
  });
  await new Promise(done => host.listen(0, '127.0.0.1', done));
  const hostOrigin = 'http://127.0.0.1:' + host.address().port;
  const token = page => page.locator('input[name="swarmmemo-gate-token"]').inputValue();
  let browser;
  try {
    browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_PATH ? {executablePath: process.env.CHROMIUM_PATH} : {})});
    // 1. Proof of work on a third-party page.
    let context = await browser.newContext({viewport: {width: 390, height: 844}});
    let page = await context.newPage();
    const errors = [], urls = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('request', request => urls.push(request.url()));
    await page.goto(hostOrigin + '/form');
    const box = page.getByRole('checkbox', {name: 'Verify: I am a real agent'});
    await box.click();
    await page.waitForFunction(() => document.querySelector('input[name="swarmmemo-gate-token"]')?.value.startsWith('smg1.'), null, {timeout: 60000});
    let p = check(await token(page), status, hostOrigin);
    assert.equal(p.passed, true); assert.deepEqual(p.proofs, ['pow']); assert.equal(p.subject, '');
    assert.equal(await page.locator('input[name="swarmmemo-gate-status"]').inputValue(), 'passed');
    assert.equal(await box.getAttribute('aria-checked'), 'true');
    assert.ok(urls.every(url => !url.includes('smg1.')), 'the token is never in a URL');
    assert.deepEqual(errors, []);

    // 2. The identity: a key this browser keeps on SwarmMemo signs in the window.
    const seed = randomBytes(32);
    const priv = createPrivateKey({key: Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), seed]), format: 'der', type: 'pkcs8'});
    const pub = createPublicKey(priv).export({format: 'der', type: 'spki'}).subarray(-32);
    const fingerprint = createHash('sha256').update(pub).digest('hex');
    const stored = {version: 1, service: 'swarmmemo.com', public_key: b64url(pub), private_key: b64url(seed), fingerprint, handle: ''};
    await context.addInitScript(({origin, stored}) => { if (location.origin === origin) localStorage.setItem('swarmmemo.identity.v1', stored); }, {origin, stored: JSON.stringify(stored)});
    page = await context.newPage();
    await page.goto(hostOrigin + '/form');
    let opened = page.waitForEvent('popup');
    await page.getByRole('button', {name: 'Use my SwarmMemo identity'}).click();
    let popup = await opened;
    await popup.waitForURL(url => url.pathname === '/gate/pass');
    assert.equal(new URL(popup.url()).searchParams.get('origin'), hostOrigin);
    await popup.locator('#gate-pass-shares').waitFor();
    assert.match(await popup.locator('.connect-embed-ask').textContent(), new RegExp(hostOrigin.replace(/[.:/]/g, '\\$&') + ' asks you to show you are a real agent'));
    await popup.locator('#gate-pass-allow:not([disabled])').waitFor();
    await popup.getByRole('button', {name: 'Allow', exact: true}).click();
    await page.waitForFunction(() => document.querySelector('input[name="swarmmemo-gate-token"]')?.value.startsWith('smg1.'), null, {timeout: 60000});
    p = check(await token(page), status, hostOrigin);
    assert.equal(p.subject, fingerprint); assert.deepEqual(p.proofs, ['pow', 'identity']);
    assert.equal(await page.evaluate(() => localStorage.getItem('swarmmemo.identity.v1')), null, 'the key never reaches the host origin');
    await context.close();

    // 3. No key: the visitor may keep a free identity; the box starts unticked.
    context = await browser.newContext({viewport: {width: 390, height: 844}});
    page = await context.newPage();
    await page.goto(hostOrigin + '/form');
    opened = page.waitForEvent('popup');
    await page.getByRole('button', {name: 'Use my SwarmMemo identity'}).click();
    popup = await opened;
    await popup.locator('#gate-pass-nokey').waitFor();
    const keep = popup.locator('#gate-pass-keep');
    assert.equal(await keep.isChecked(), false, 'consent is never assumed');
    await keep.check();
    await popup.locator('#gate-pass-allow:not([disabled])').waitFor();
    const registered = popup.waitForResponse(r => r.url().endsWith('/v1/command?ref=gate'));
    await popup.getByRole('button', {name: 'Verify', exact: true}).click();
    assert.equal((await (await registered).json()).ok, true, 'the new key is registered, its arrival the gate');
    await page.waitForFunction(() => document.querySelector('input[name="swarmmemo-gate-token"]')?.value.startsWith('smg1.'), null, {timeout: 60000});
    p = check(await token(page), status, hostOrigin);
    assert.equal(p.subject.length, 64); assert.deepEqual(p.proofs, ['pow', 'identity']);
    assert.equal((await fetch(origin + '/api/agent/' + p.subject)).status, 200, 'the kept identity exists');

    // 4. The iframe button posts the token to the framing page only.
    await page.goto(hostOrigin + '/frame');
    await page.frameLocator('#g').getByRole('checkbox').click();
    await page.waitForFunction(() => document.querySelector('input[name="swarmmemo-gate-token"]')?.value.startsWith('smg1.'), null, {timeout: 60000});
    check(await token(page), status, hostOrigin);

    // 4b. The same frame on another origin's page: the work runs, but the
    // frame addresses the token to the named origin, so the page gets nothing.
    await page.goto(hostOrigin.replace('127.0.0.1', 'localhost') + '/evil-frame');
    await page.frameLocator('#g').getByRole('checkbox').click();
    await page.frameLocator('#g').getByText('Verified').waitFor({timeout: 60000});
    await page.waitForTimeout(300);
    assert.ok((await page.evaluate(() => window.got)).every(m => !m.includes('smg1.')), 'a framing page on another origin never receives the token');

    // 5. The gate is down: closed keeps the form waiting, open lets it through.
    await page.goto(hostOrigin + '/down-closed');
    await page.getByText('Verification is unavailable right now. Try again shortly.').waitFor();
    assert.equal(await page.locator('input[name="swarmmemo-gate-status"]').inputValue(), 'unavailable');
    await page.goto(hostOrigin + '/down-open');
    await page.getByText('this site lets you continue').waitFor();
    assert.equal(await token(page), '');

    // 6. /gate: the snippet generator writes the visitor's values.
    await page.goto(origin + '/gate');
    await page.locator('#gate-generator select[name="kind"]').selectOption('frame');
    await page.locator('#gate-generator input[name="min"]').fill('500');
    await page.locator('#gate-generator input[name="origin"]').fill('https://shop.example/signup');
    const snippet = await page.locator('#gate-snippet').textContent();
    assert.match(snippet, /gate\/frame\?origin=https:\/\/shop\.example&min=500&fail=closed/);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    assert.ok(overflow <= 0, 'no horizontal scroll at phone width');

    // 7. /gate/agent by hand: the clock runs out.
    await page.goto(origin + '/gate/agent');
    await page.getByRole('button', {name: 'Start the clock'}).click();
    await page.locator('#gate-agent-records span').first().waitFor();
    assert.equal(await page.locator('#gate-agent-records span').count(), 120);
    await page.waitForTimeout(3200);
    await page.locator('#gate-agent-answer').fill('12345');
    await page.getByRole('button', {name: 'Answer', exact: true}).click();
    await page.locator('#gate-agent-status').filter({hasText: /Too slow|does not hold/}).waitFor();
    await context.close();
    console.log('PASS: the SwarmMemo Gate: work, identity, keep, frame, fail closed and open, generator, agent task.');
  } finally {
    await browser?.close();
    host.close();
    up.proc.kill(); down.proc.kill();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
