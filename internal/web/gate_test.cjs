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
// C348: a site that asks for standing refuses a fresh browser and shows the
// ways through; invisible mode passes silently, and opens a focus-trapped
// dialog when the site asks for more; earning standing in the browser
// reaches a small minimum on a third server whose trust parameters price
// proof of work at a test-only rate (one unit a cent, set with the
// operator's params command), counted by a trust run; /gate's playground
// shows the server's decision; the share card renders.
const assert = require('node:assert/strict');
const http = require('node:http');
const net = require('node:net');
const {spawn, execFileSync} = require('node:child_process');
const {randomBytes, createPrivateKey, createPublicKey, createHash, verify} = require('node:crypto');
const {mkdtempSync, writeFileSync} = require('node:fs');
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
  return {origin, proc, data: join(dir, 'data')};
}
// operator runs the server binary's operator command on a board's data.
const operator = (b, ...args) => execFileSync(process.env.SWARMMEMO_TEST_BINARY, args, {env: {...process.env, DATA_DIR: b.data, SERVICE_ID: 'swarmmemo.com', TRUST: 'on'}}).toString();
const shots = process.env.SWARMMEMO_SCREENSHOT_DIR;
async function shoot(page, name, target) {
  if (!shots) return;
  for (const [label, width] of [['desktop', 1280], ['phone', 390]]) {
    await page.setViewportSize({width, height: 900});
    await (target ? target() : page).screenshot({path: join(shots, 'gate-' + name + '-' + label + '.png'), ...(target ? {} : {fullPage: true})});
  }
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
  // The third server counts standing, and prices proof of work at one unit
  // (2^20 hashes) a cent: test-only parameters, so a browser reaches a small
  // minimum in seconds. The real price (/v1/gate/status) is millions of
  // units a cent, which the earn window says.
  const trusted = await board({SERVICES: 'notary', TRUST: 'on'});
  const params = JSON.parse(operator(trusted, 'trust', 'params'));
  params.proofs.pow.units_per_cent = 1;
  const paramsFile = join(mkdtempSync(join(tmpdir(), 'gate-params-')), 'trust.json');
  writeFileSync(paramsFile, JSON.stringify(params));
  operator(trusted, 'params', 'set', 'trust', paramsFile, '--reason', 'gate browser suite: test-only proof-of-work price');
  const origin = up.origin;
  const status = await (await fetch(origin + '/v1/gate/status')).json();
  assert.equal(status.ok, true);
  assert.equal((await (await fetch(down.origin + '/v1/gate/status')).json()).ok, false);

  // The host site: one form per case, under a policy that names SwarmMemo.
  const pages = {
    '/form': `<form id="f" action="/signup" method="post"><input name="email"><script src="${origin}/gate/v1.js" async data-min="0" data-fail="closed"></script><button>Sign up</button></form>`,
    '/down-closed': `<form id="f"><script src="${down.origin}/gate/v1.js" async data-fail="closed"></script></form>`,
    '/down-open': `<form id="f"><script src="${down.origin}/gate/v1.js" async data-fail="open"></script></form>`,
    '/backed': `<form id="f"><script src="${origin}/gate/v1.js" async data-min="50"></script></form>`,
    '/invisible': `<form id="f" action="/done" method="post"><input name="email" aria-label="Email"><script src="${origin}/gate/v1.js" async data-mode="invisible"></script><button>Sign up</button></form>`,
    '/invisible-backed': `<form id="f" action="/done" method="post"><input name="email" aria-label="Email"><script src="${trusted.origin}/gate/v1.js" async data-mode="invisible" data-min="10"></script><button>Sign up</button></form>`,
    '/frame': `<iframe id="g" src="${origin}/gate/frame?origin=ORIGIN&min=0&fail=closed" title="SwarmMemo Gate" width="340" height="96" style="border:0"></iframe>
      <form id="f"><input type="hidden" name="swarmmemo-gate-token"></form><script src="/listen.js"></script>`,
  };
  // C325-sec: a page on another origin that frames the button for this
  // host's origin (localhost is another origin than 127.0.0.1) records
  // every message it gets: the token must never reach it.
  pages['/evil-frame'] = `<iframe id="g" src="${origin}/gate/frame?origin=ORIGIN&min=0&fail=closed" title="SwarmMemo Gate" width="340" height="96" style="border:0"></iframe><script src="/record.js"></script>`;
  const posted = [];
  const host = http.createServer((request, response) => {
    if (request.url === '/done' && request.method === 'POST') {
      let body = '';
      request.on('data', chunk => { body += chunk; });
      request.on('end', () => { posted.push(new URLSearchParams(body)); response.setHeader('Content-Type', 'text/html'); response.end('<!doctype html><title>Done</title><p>Signed up.</p>'); });
      return;
    }
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
    response.setHeader('Content-Security-Policy', `default-src 'self'; script-src 'self' ${origin} ${down.origin} ${trusted.origin}; connect-src ${origin} ${down.origin} ${trusted.origin}; frame-src ${origin}; style-src 'self'; object-src 'none'`);
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

    // 8. C348: a site that asks for standing, in a fresh browser: the gate
    // refuses the work, and the widget says why and offers the ways through.
    context = await browser.newContext({viewport: {width: 390, height: 844}});
    page = await context.newPage();
    await page.goto(hostOrigin + '/backed');
    await page.getByRole('checkbox', {name: 'Show SwarmMemo standing of $0.50'}).click();
    await page.getByText('Refused: this site asks for $0.50 of standing.').waitFor({timeout: 60000});
    assert.equal(await page.locator('input[name="swarmmemo-gate-status"]').inputValue(), 'refused');
    assert.equal(await token(page), '', 'no token: the gate refused the work');
    for (const way of ['Use my SwarmMemo identity', 'Link a wallet', 'Earn standing in your browser']) {
      assert.ok(await page.getByRole('button', {name: new RegExp('^' + way)}).isVisible(), way + ' is offered');
    }
    assert.match(await page.locator('.ways').textContent(), /anyone can repeat it; standing is collateral/);
    await shoot(page, 'backed-refusal-host');

    // 9. Invisible: nothing but a line until submit; the work runs from the
    // first focus, and the form goes through with the token.
    await page.goto(hostOrigin + '/invisible');
    assert.equal(await page.getByRole('checkbox').count(), 0, 'invisible shows no box');
    await page.getByText('Protected by').waitFor();
    await page.getByLabel('Email').fill('agent@example.com');
    await Promise.all([page.waitForURL(hostOrigin + '/done'), page.getByRole('button', {name: 'Sign up'}).click()]);
    const silent = posted.at(-1);
    assert.equal(silent.get('email'), 'agent@example.com');
    p = check(silent.get('swarmmemo-gate-token'), status, hostOrigin);
    assert.equal(p.passed, true); assert.deepEqual(p.proofs, ['pow']);
    await context.close();

    // 10. Invisible on a site that asks for $0.10: submit opens a modal
    // dialog with the ways through (focus inside, Tab stays, Escape closes);
    // earning standing in the browser reaches $0.10, the trust run counts
    // it, and passing as the new key sends the form.
    const trustedStatus = await (await fetch(trusted.origin + '/v1/gate/status')).json();
    assert.equal(trustedStatus.standing.pow.units_per_cent, 1, 'the test-only price is in effect');
    context = await browser.newContext({viewport: {width: 1280, height: 900}});
    page = await context.newPage();
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(hostOrigin + '/invisible-backed');
    await page.getByLabel('Email').fill('earner@example.com');
    await page.getByRole('button', {name: 'Sign up'}).click();
    const dialog = page.getByRole('dialog');
    await dialog.waitFor({timeout: 60000});
    assert.match(await dialog.textContent(), /This site asks for \$0\.10 of standing/);
    const focused = () => page.evaluate(() => document.querySelector('.swarmmemo-gate').shadowRoot.activeElement?.textContent || '');
    assert.match(await focused(), /^Use my SwarmMemo identity/);
    for (let i = 0; i < 6; i++) await page.keyboard.press('Tab');
    assert.ok(await page.evaluate(() => { const r = document.querySelector('.swarmmemo-gate').shadowRoot; return r.querySelector('dialog').contains(r.activeElement); }), 'focus stays in the dialog');
    await shoot(page, 'invisible-dialog');
    await page.keyboard.press('Escape');
    await dialog.waitFor({state: 'hidden'});
    assert.equal(posted.length, 1, 'nothing was submitted');
    await page.getByRole('button', {name: 'Sign up'}).click();
    await dialog.waitFor();
    opened = page.waitForEvent('popup');
    await dialog.getByRole('button', {name: /^Earn standing in your browser/}).click();
    popup = await opened;
    await popup.waitForURL(url => url.pathname === '/gate/pass' && url.searchParams.get('way') === 'earn');
    await popup.locator('#gate-earn-plan').filter({hasText: /reaches \$0\.10/}).waitFor({timeout: 30000});
    const start = popup.getByRole('button', {name: /^Start/});
    assert.equal(await start.isDisabled(), true, 'no key without the box ticked');
    await popup.locator('#gate-pass-keep').check();
    await popup.locator('#gate-pass-allow:not([disabled])').waitFor();
    await start.click();
    await popup.locator('#gate-earn-progress').waitFor();
    await shoot(popup, 'earn-progress');
    await popup.getByRole('button', {name: 'Pass as yourself'}).waitFor({timeout: 120000});
    assert.match(await popup.locator('#gate-earn-plan').textContent(), /enough for this site once the next trust run counts it/);
    // The nightly run counts work done before its midnight: the fixture
    // dates this key's work and arrival a day back, then runs it.
    execFileSync('python3', ['-c', 'import sqlite3, sys; db = sqlite3.connect(sys.argv[1], timeout=30); db.execute("UPDATE standing_assessments SET created_at=created_at-86400, assessed_at=assessed_at-86400"); db.execute("UPDATE audit SET created_at=created_at-86400"); db.commit()', join(trusted.data, 'swarmmemo.db')]);
    assert.match(operator(trusted, 'trust', 'run'), /state=done/);
    await Promise.all([page.waitForURL(hostOrigin + '/done', {timeout: 60000}), popup.getByRole('button', {name: 'Pass as yourself'}).click()]);
    const earned = posted.at(-1);
    assert.equal(earned.get('email'), 'earner@example.com');
    p = check(earned.get('swarmmemo-gate-token'), {...status, ...trustedStatus}, hostOrigin);
    assert.equal(p.passed, true); assert.ok(p.standing_cents >= 10, 'earned standing ' + p.standing_cents); assert.ok(p.proofs.includes('identity'));
    await context.close();

    // 11. /gate's playground: Backed refuses a fresh browser, and the
    // decision beside the token says why; Open passes and is accepted.
    context = await browser.newContext({viewport: {width: 1280, height: 900}});
    page = await context.newPage();
    await page.goto(origin + '/gate');
    await page.getByRole('checkbox', {name: 'Verify: I am a real agent'}).click();
    await page.locator('#gate-demo-decision').filter({hasText: 'A server at Open accepts: passed true'}).waitFor({timeout: 60000});
    await shoot(page, 'open-pass', () => page.locator('#demo'));
    await page.locator('#gate-strictness input[value="backed"]').check();
    await page.locator('#gate-strictness select[name="min"]').selectOption('100');
    assert.equal(await page.locator('#gate-demo-attrs').textContent(), 'data-min="100" data-fail="closed"');
    await page.getByRole('checkbox', {name: 'Show SwarmMemo standing of $1'}).click();
    await page.locator('#gate-demo-decision').filter({hasText: 'A server at Backed ($1) refuses'}).waitFor({timeout: 60000});
    assert.ok(await page.getByRole('button', {name: /^Earn standing in your browser/}).isVisible());
    await shoot(page, 'backed-refusal', () => page.locator('#demo'));
    // The generator writes the strictness options.
    await page.locator('#gate-generator select[name="bits"]').selectOption('24');
    await page.locator('#gate-generator select[name="mode"]').selectOption('invisible');
    assert.match(await page.locator('#gate-snippet').textContent(), /data-min="0" data-bits="24" data-mode="invisible" data-fail="closed"/);

    // 12. The share card: an agent passes the task in code, and its card
    // renders at 1200 by 630.
    const challenge = (await (await fetch(origin + '/v1/gate/challenge', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: '{"schema":1,"mode":"agent"}'})).json()).data;
    const sum = challenge.task.records.filter(r => r.tag === challenge.task.where.tag).reduce((a, r) => a + r.value, 0);
    const solved = (await (await fetch(origin + '/v1/gate/verify', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({schema: 1, challenge: challenge.challenge, answer: String(sum)})})).json()).data;
    await page.goto(origin + solved.card.url);
    await page.locator('img.gate-card').evaluate(img => img.decode());
    assert.deepEqual(await page.locator('img.gate-card').evaluate(img => [img.naturalWidth, img.naturalHeight]), [1200, 630]);
    if (shots) { const png = await (await fetch(origin + solved.card.image)).arrayBuffer(); writeFileSync(join(shots, 'gate-card.png'), Buffer.from(png)); }
    await shoot(page, 'card-page');
    await context.close();
    assert.deepEqual(errors, []);
    console.log('PASS: the SwarmMemo Gate: work, identity, keep, frame, fail closed and open, generator, agent task, refusal and ways through, invisible, earned standing, playground, card.');
  } finally {
    await browser?.close();
    host.close();
    up.proc.kill(); down.proc.kill(); trusted.proc.kill();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
