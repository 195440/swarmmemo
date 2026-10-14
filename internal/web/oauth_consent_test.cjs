'use strict';
// The OAuth sign-in page in a real browser (internal/httpapi/oauth.go): it
// runs its own server from the owned test binary with hosted identities on
// and PUBLIC_URL set to it, registers a loopback client, signs in with a new
// identity (handle, recovery code, the required "saved" box, Continue), checks
// the redirect back carries the code and state, exchanges it, calls /mcp as
// the identity, then reconnects in one click as a returning grant.
const assert = require('node:assert/strict');
const http = require('node:http');
const net = require('node:net');
const {spawn} = require('node:child_process');
const {createHash, randomBytes} = require('node:crypto');
const {mkdtempSync, writeFileSync} = require('node:fs');
const {join} = require('node:path');
const {tmpdir} = require('node:os');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
assert.ok(process.env.SWARMMEMO_TEST_BINARY, 'owned test binary required');

const freePort = () => new Promise((resolve, reject) => {
  const s = net.createServer();
  s.listen(0, '127.0.0.1', () => {const {port} = s.address(); s.close(() => resolve(port));});
  s.on('error', reject);
});
const b64url = buf => buf.toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');

(async () => {
  const dir = mkdtempSync(join(tmpdir(), 'oauth-consent-'));
  const kek = join(dir, 'kek');
  writeFileSync(kek, b64url(randomBytes(32)) + '\n', {mode: 0o600});
  const port = await freePort(), appPort = await freePort();
  const origin = 'http://127.0.0.1:' + port, redirect = 'http://127.0.0.1:' + appPort + '/cb';
  const server = spawn(process.env.SWARMMEMO_TEST_BINARY, [], {
    env: {...process.env, DATA_DIR: join(dir, 'data'), HOSTED_KEK_FILE: kek, PUBLIC_URL: origin, SERVICE_ID: 'swarmmemo.com', ALLOW_INSECURE_LOCAL: 'true', LISTEN_ADDR: '127.0.0.1:' + port},
    stdio: 'ignore',
  });
  const callbacks = [];
  const app = http.createServer((req, res) => {const u = new URL(req.url, 'http://127.0.0.1:' + appPort); if (u.pathname === '/cb') callbacks.push(u); res.end('back in the app');});
  await new Promise(resolve => app.listen(appPort, '127.0.0.1', resolve));
  let browser;
  try {
    for (let i = 0; ; i++) {
      try {if ((await fetch(origin + '/.well-known/oauth-authorization-server')).ok) break;} catch {}
      assert.ok(i < 80, 'server did not start');
      await new Promise(resolve => setTimeout(resolve, 250));
    }
    const reg = await (await fetch(origin + '/oauth/register', {method: 'POST', headers: {'content-type': 'application/json'},
      body: JSON.stringify({client_name: 'Browser Test App', redirect_uris: [redirect], token_endpoint_auth_method: 'none'})})).json();
    assert.match(reg.client_id, /^smcl_/);
    const verifier = b64url(randomBytes(48));
    const challenge = b64url(createHash('sha256').update(verifier).digest());
    const authorize = state => origin + '/oauth/authorize?' + new URLSearchParams({response_type: 'code', client_id: reg.client_id, redirect_uri: redirect,
      state, code_challenge: challenge, code_challenge_method: 'S256', scope: 'hosted', resource: origin + '/mcp'});

    browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_PATH ? {executablePath: process.env.CHROMIUM_PATH} : {})});
    const page = await (await browser.newContext({viewport: {width: 390, height: 844}})).newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const response = await page.goto(authorize('st-1'));
    assert.equal(response.status(), 200);
    assert.equal(response.headers()['x-frame-options'], 'DENY');
    assert.match(response.headers()['content-security-policy'], /frame-ancestors 'none'/);
    await page.getByRole('heading', {name: 'Connect Browser Test App to SwarmMemo'}).waitFor();
    assert.ok(await page.getByText('This assistant gets its own SwarmMemo identity.').isVisible());
    assert.ok(await page.getByText('every SwarmMemo tool').isVisible());
    assert.ok(await page.getByText('runs on your own computer').isVisible(), 'loopback warning');
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    assert.ok(overflow <= 0, 'no horizontal scroll at phone width');

    const handle = 'bt-' + randomBytes(3).toString('hex');
    await page.getByLabel('Handle (optional)').fill(handle);
    await page.getByRole('button', {name: 'Create an identity and connect'}).click();
    await page.getByRole('heading', {name: 'Save your recovery code'}).waitFor();
    const recovery = (await page.locator('.oauth-recovery code').textContent()).trim();
    assert.match(recovery, /^smr1_[A-Za-z0-9_-]{43}$/);
    // Continue needs the box ticked.
    await page.getByRole('button', {name: 'Continue to Browser Test App'}).click();
    await page.waitForTimeout(300);
    assert.equal(callbacks.length, 0, 'continued without confirming the recovery code was saved');
    await page.getByLabel('I saved my recovery code').check();
    await page.getByRole('button', {name: 'Continue to Browser Test App'}).click();
    await page.getByText('back in the app').waitFor();
    assert.equal(callbacks.length, 1);
    const back = callbacks[0];
    assert.equal(back.searchParams.get('state'), 'st-1');
    assert.equal(back.searchParams.get('iss'), origin);
    assert.match(back.searchParams.get('code'), /^smc1_/);

    const tokens = await (await fetch(origin + '/oauth/token', {method: 'POST', headers: {'content-type': 'application/x-www-form-urlencoded'},
      body: new URLSearchParams({grant_type: 'authorization_code', code: back.searchParams.get('code'), client_id: reg.client_id, redirect_uri: redirect, code_verifier: verifier, resource: origin + '/mcp'})})).json();
    assert.match(tokens.access_token || '', /^smh1_/, JSON.stringify(tokens));
    const call = await fetch(origin + '/mcp', {method: 'POST', headers: {'content-type': 'application/json', accept: 'application/json, text/event-stream', authorization: 'Bearer ' + tokens.access_token},
      body: JSON.stringify({jsonrpc: '2.0', id: 1, method: 'tools/call', params: {name: 'whoami', arguments: {}}})});
    const whoami = await call.text();
    assert.ok(whoami.includes(handle), 'whoami on /mcp as the signed-in identity: ' + whoami.slice(0, 300));

    // A returning grant: one click, no new recovery code.
    await page.goto(authorize('st-2'));
    await page.getByRole('heading', {name: 'Welcome back'}).waitFor();
    await page.getByRole('button', {name: 'Continue as @' + handle}).click();
    await page.getByText('back in the app').waitFor();
    assert.equal(callbacks.length, 2);
    assert.equal(callbacks[1].searchParams.get('state'), 'st-2');
    assert.deepEqual(errors, []);
    console.log('PASS: OAuth sign-in page creates, continues, returns and reconnects in a browser.');
  } finally {
    if (browser) await browser.close();
    app.close();
    server.kill();
  }
})().catch(error => {console.error(error); process.exit(1);});
