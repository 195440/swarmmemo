/* SwarmMemo Gate widget (C325): a bot check in place of a CAPTCHA.
   On another site it is /gate/v1.js, this file with gate-pow.js inside one
   closure: a <script data-...> inside a form adds the check, and a pass puts
   the token in a hidden input (data-name, default swarmmemo-gate-token) and
   fires a swarmmemo-gate event. On swarmmemo.com it also runs the iframe
   button (/gate/frame), which posts the result to the page that framed it,
   addressed to that page's origin only. No cookies, no storage, no globals
   on the host page. The token travels in request and message bodies only.
   C348: a site that asks for standing (data-min) refuses work alone, as the
   gate does, and the widget says why and offers the ways through, each in
   the first-party /gate/pass window (an identity, a wallet linked to it, or
   standing earned with proof of work). data-mode="invisible" shows a line
   only: the work starts when the form is first focused, the form submits
   with the token, and a refusal opens a modal dialog with the same ways. */
(() => {
  'use strict';
  const me = document.currentScript;
  if (!me) return;
  const SM = new URL(me.src).origin;
  // The frame button: this file on swarmmemo.com's own /gate/frame page.
  const frame = SM === location.origin && document.body?.dataset.gateFrame === '1';
  const conf = frame ? document.body.dataset : me.dataset;
  // The origin the token is for: this page's, or the framing page's.
  const site = frame ? conf.origin : location.origin;
  const min = Math.max(0, Math.min(1000000, parseInt(conf.min || '0', 10) || 0));
  const failOpen = conf.fail === 'open';
  const bits = parseInt(conf.bits || '0', 10) || 0;
  const nonce = /^[A-Za-z0-9_-]{8,64}$/.test(conf.nonce || '') ? conf.nonce : '';
  const name = /^[A-Za-z0-9_-]{1,64}$/.test(conf.name || '') ? conf.name : 'swarmmemo-gate-token';
  const invisible = !frame && conf.mode === 'invisible';
  const dollars = cents => '$' + (cents / 100).toFixed(cents % 100 ? 2 : 0);

  // Where it renders and delivers.
  const host = document.createElement('div');
  host.className = 'swarmmemo-gate';
  if (frame) document.getElementById('gate-frame').append(host);
  else me.after(host);
  const form = frame ? null : me.closest('form') || (conf.form && document.querySelector(conf.form));
  const hidden = (field, value) => {
    if (!form) return;
    let input = form.querySelector('input[type=hidden][name="' + field + '"]');
    if (!input) { input = document.createElement('input'); input.type = 'hidden'; input.name = field; form.append(input); }
    input.value = value;
  };
  function deliver(detail) {
    if (frame) { try { parent.postMessage({type: 'swarmmemo-gate', ...detail}, site); } catch (_) { /* No parent. */ } return; }
    hidden(name, detail.token || '');
    hidden('swarmmemo-gate-status', detail.status);
    (form || me).dispatchEvent(new CustomEvent('swarmmemo-gate', {bubbles: true, detail}));
    const callback = conf.callback && /^[A-Za-z_$][\w$]*$/.test(conf.callback) ? window[conf.callback] : null;
    if (typeof callback === 'function') { try { callback(detail); } catch (_) { /* The site's own code. */ } }
  }

  const root = host.attachShadow ? host.attachShadow({mode: 'open'}) : host;
  const css = `:host{all:initial;display:block;font:14px/1.4 system-ui,sans-serif;color:var(--sm-ink,#1f2937)}
.box{display:flex;align-items:center;gap:12px;max-width:320px;padding:12px 14px;border:1px solid var(--sm-border,#d1d5db);border-radius:8px;background:var(--sm-bg,#fff)}
button{font:inherit;cursor:pointer}.check{flex:none;width:26px;height:26px;border:2px solid #6b7280;border-radius:5px;background:#fff;display:grid;place-items:center;padding:0}
.check[aria-checked=true]{border-color:#15803d;background:#15803d;color:#fff}.check.no{border-color:#b91c1c;color:#b91c1c}.main{flex:1;min-width:0}.label{font-weight:600}
.status{font-size:12px;color:#6b7280;margin-top:2px}.bar{height:4px;margin-top:6px;border-radius:2px;background:#e5e7eb;overflow:hidden}.bar i{display:block;height:100%;width:0;background:#2563eb;transition:width .2s}
.id{border:0;background:none;color:#2563eb;padding:0;font-size:12px;text-decoration:underline;text-align:left}.brand{font-size:11px;color:#6b7280;text-align:right}.brand a{color:inherit}
.line{font-size:12px;color:#6b7280}.line a{color:inherit}.ways{max-width:320px;margin-top:8px;padding:12px 14px;border:1px solid #fca5a5;border-radius:8px;background:#fff;color:#1f2937}
.ways b{display:block;color:#b91c1c}.ways p{margin:4px 0 8px;font-size:13px}.ways button{display:block;width:100%;margin:6px 0 0;padding:8px 10px;border:1px solid #d1d5db;border-radius:6px;background:#f9fafb;color:#1f2937;text-align:left}
.ways button:hover,.ways button:focus-visible{border-color:#2563eb;outline:2px solid #2563eb;outline-offset:1px}.ways small{display:block;color:#6b7280;font-size:12px}.ways a{font-size:12px;color:#2563eb}
dialog{max-width:360px;width:calc(100% - 32px);padding:0;border:0;border-radius:10px;background:transparent}dialog::backdrop{background:rgba(15,23,42,.55)}dialog .ways{max-width:none;margin:0;border-color:#d1d5db}
dialog .close{text-align:center;color:#4b5563;background:#fff}`;
  // A constructed sheet, as the comment embed uses: no inline style for a
  // page's Content Security Policy to refuse.
  if ('adoptedStyleSheets' in root && typeof CSSStyleSheet.prototype.replaceSync === 'function') {
    const sheet = new CSSStyleSheet(); sheet.replaceSync(css); root.adoptedStyleSheets = [sheet];
  } else { const style = document.createElement('style'); style.textContent = css; root.append(style); }
  const el = (tag, cls, text) => { const n = document.createElement(tag); if (cls) n.className = cls; if (text !== undefined) n.textContent = text; return n; };
  const box = el('div', 'box'), check = el('button', 'check'), main = el('div', 'main'), label = el('div', 'label'), status = el('div', 'status'), bar = el('div', 'bar'), fill = el('i');
  const idButton = el('button', 'id', 'Use my SwarmMemo identity'), brand = el('div', 'brand'), link = el('a', '', 'SwarmMemo Gate');
  check.type = idButton.type = 'button';
  check.setAttribute('role', 'checkbox'); check.setAttribute('aria-checked', 'false');
  link.href = SM + '/gate'; link.target = '_blank'; link.rel = 'noopener';
  bar.append(fill); bar.hidden = true; status.setAttribute('role', 'status');
  if (invisible) {
    // A line, not a box: what guards the form, and what it is doing.
    const line = el('div', 'line', 'Protected by ');
    line.append(link, ' · no tracking. ', status);
    root.append(line);
  } else {
    brand.append(link); main.append(label, status, bar, idButton);
    box.append(check, main, brand); root.append(box);
  }
  label.textContent = min > 0 ? 'Show SwarmMemo standing of ' + dollars(min) : 'Verify: I am a real agent';
  check.setAttribute('aria-label', label.textContent);
  if (min > 0) idButton.textContent = 'Sign in with SwarmMemo to verify';
  // state: idle, busy, passed, refused or failed; want: the visitor
  // submitted (invisible) and the form should go once it may.
  let state = 'idle', expires = 0, want = null, panel = null, dialog = null;
  const say = text => { status.textContent = text; };

  async function post(path, body) {
    const response = await fetch(SM + path, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body), credentials: 'omit', referrerPolicy: 'no-referrer', cache: 'no-store'});
    const result = await response.json().catch(() => null);
    if (!response.ok || result?.ok !== true) throw Object.assign(Error(result?.error?.message || 'The gate did not answer. Try again.'), {code: result?.error?.code, status: response.status});
    return result.data;
  }
  function worker() {
    try { if (location.origin === SM) return new Worker(SM + '/assets/gate-pow.js'); } catch (_) { /* Below. */ }
    try { if (typeof powSource === 'string') return new Worker(URL.createObjectURL(new Blob([powSource], {type: 'text/javascript'}))); } catch (_) { /* The site's CSP: solve on the page. */ }
    return null;
  }
  function solve(prefix, n, progress) {
    const w = worker();
    if (!w) return SwarmMemoGatePow.solve(prefix, n, progress);
    return new Promise((resolve, reject) => {
      w.onmessage = event => {
        const m = event.data || {};
        if (typeof m.progress === 'number') progress(m.progress);
        if (m.solution) { w.terminate(); resolve(m.solution); }
        if (m.error) { w.terminate(); reject(Error(m.error)); }
      };
      w.onerror = () => { w.terminate(); SwarmMemoGatePow.solve(prefix, n, progress).then(resolve, reject); };
      w.postMessage({prefix, bits: n});
    });
  }
  // go submits the form the visitor asked to submit, once it may.
  function go() {
    const s = want; want = null;
    if (s && form && me.isConnected) form.requestSubmit(s.form === form ? s : undefined);
  }
  function passed(data) {
    if (!data.passed) return refuse('Your standing, ' + dollars(data.standing_cents) + ', is below this site\'s ' + dollars(min) + '.', data);
    state = 'passed'; expires = data.expires_at * 1000; bar.hidden = true; panel?.remove(); dialog?.close();
    check.className = 'check'; check.setAttribute('aria-checked', 'true'); check.textContent = '✓'; check.disabled = true;
    say(data.subject && data.proofs.includes('identity') ? 'Verified as your SwarmMemo identity · standing ' + dollars(data.standing_cents) : 'Verified');
    deliver({status: 'passed', token: data.token, passed: true, standing_cents: data.standing_cents, proofs: data.proofs, expires_at: data.expires_at});
    go();
  }
  // refuse says why and offers the ways through: inline under the box, or
  // in a modal dialog when invisible.
  function refuse(why, data) {
    state = 'refused'; bar.hidden = true; check.disabled = false;
    check.className = 'check no'; check.setAttribute('aria-checked', 'false'); check.textContent = '✕';
    say(invisible ? 'This site asks for standing.' : 'Refused: this site asks for ' + dollars(min) + ' of standing.');
    deliver(data ? {status: 'below_minimum', token: data.token, passed: false, standing_cents: data.standing_cents, proofs: data.proofs, expires_at: data.expires_at} : {status: 'refused', code: 'standing_required'});
    panel?.remove();
    if (frame) { idButton.textContent = 'Ways through'; return; }
    panel = el('div', 'ways');
    panel.id = 'ways';
    const title = el('b', '', 'This site asks for ' + dollars(min) + ' of standing');
    title.id = 'ways-title';
    panel.append(title, el('p', '', why + ' Proof of work shows a real browser, but anyone can repeat it; standing is collateral an agent holds on SwarmMemo and could lose. Ways through:'));
    const way = (text, note, kind) => { const b = el('button', '', text); b.type = 'button'; b.append(el('small', '', note)); b.onclick = () => identity(kind); panel.append(b); };
    way('Use my SwarmMemo identity', 'Passes now if your key has the standing.', '');
    way('Link a wallet', 'Adds standing at the next nightly trust run.', 'wallet');
    way('Earn standing in your browser', 'A longer proof of work; it says first what it can reach.', 'earn');
    const all = el('a', '', 'Every way to raise standing'); all.href = SM + '/me#standing'; all.target = '_blank'; all.rel = 'noopener';
    panel.append(all);
    if (!invisible) { root.append(panel); return; }
    if (want) show();
  }
  function show() {
    if (!dialog) {
      dialog = el('dialog');
      dialog.setAttribute('aria-labelledby', 'ways-title');
      // Escape closes it (cancel); so does Not now. Nothing here confirms
      // anything: each way opens the swarmmemo.com window.
      dialog.addEventListener('close', () => { want = null; });
      // Tab stays inside, round and round.
      dialog.addEventListener('keydown', e => {
        if (e.key !== 'Tab') return;
        const f = [...dialog.querySelectorAll('button,a[href]')], i = f.indexOf(root.activeElement || document.activeElement);
        if (e.shiftKey ? i <= 0 : i === f.length - 1) { e.preventDefault(); f[e.shiftKey ? f.length - 1 : 0].focus(); }
      });
      root.append(dialog);
    }
    if (!panel.querySelector('.close')) { const close = el('button', 'close', 'Not now'); close.type = 'button'; close.onclick = () => dialog.close(); panel.append(close); }
    dialog.replaceChildren(panel);
    if (typeof dialog.showModal === 'function') dialog.showModal(); else dialog.setAttribute('open', '');
    panel.querySelector('button')?.focus();
  }
  function failed(error) {
    state = 'failed'; bar.hidden = true; check.disabled = false;
    if (error.status === undefined || error.status >= 500) return unavailable();
    if (error.code === 'standing_required') return refuse('Work alone does not pass here.');
    say(error.message + (invisible ? ' Submit again to retry.' : ' Click to try again.'));
    deliver({status: 'failed'});
    want = null;
  }
  function unavailable() {
    state = 'failed'; bar.hidden = true;
    say(failOpen ? 'Verification is unavailable right now; this site lets you continue.' : 'Verification is unavailable right now. Try again shortly.');
    deliver({status: 'unavailable', fail: failOpen ? 'open' : 'closed'});
    if (failOpen && want) { state = 'passed'; expires = Infinity; go(); } else want = null;
  }
  async function work() {
    if (state === 'busy' || state === 'passed') return;
    state = 'busy'; check.disabled = true; bar.hidden = invisible; fill.style.width = '0'; panel?.remove(); say('Asking for a challenge…');
    try {
      // The page's origin: SwarmMemo checks it against the browser's Origin
      // header, so a page can only ask for its own.
      const body = {schema: 1, origin: site};
      if (min) body.min = min;
      if (nonce) body.nonce = nonce;
      if (bits) body.bits = bits;
      const ch = await post('/v1/gate/challenge', body);
      say(invisible ? 'Checking this browser…' : 'Working… a few seconds');
      const solution = await solve(ch.pow.prefix, ch.pow.bits, p => { fill.style.width = Math.round(p * 100) + '%'; });
      say('Checking…');
      passed(await post('/v1/gate/verify', {schema: 1, challenge: ch.challenge, solution}));
    } catch (error) { failed(error); }
  }
  // The identity: a swarmmemo.com window signs with the visitor's own key and
  // answers this page only (it checks who opened it, as Sign in does). way
  // opens it on a way through: wallet or earn.
  let win = null;
  function identity(way) {
    if (state === 'passed' || win && !win.closed) return win?.focus();
    const params = new URLSearchParams({origin: site});
    if (min) params.set('min', String(min));
    if (nonce) params.set('nonce', nonce);
    if (bits) params.set('bits', String(bits));
    if (frame) params.set('via', 'frame');
    if (way) params.set('way', way);
    win = open(SM + '/gate/pass?' + params, 'swarmmemo-gate', 'popup,width=480,height=' + (way ? 760 : 640));
    if (!win) { say('Allow pop-ups for this site to use your SwarmMemo identity.'); return; }
    say('Finish in the SwarmMemo window.');
    const stop = () => { removeEventListener('message', listen); clearInterval(watch); };
    const watch = setInterval(() => { if (win.closed) { stop(); if (state !== 'passed') say('The SwarmMemo window closed.'); } }, 500);
    function listen(event) {
      if (event.source !== win || event.origin !== SM || !event.data || typeof event.data !== 'object') return;
      const m = event.data;
      if (m.type === 'swarmmemo-gate-ready') { win.postMessage({type: 'swarmmemo-gate-hello'}, SM); return; }
      if (m.type === 'swarmmemo-gate-cancel') { stop(); say('Cancelled.'); return; }
      if (m.type !== 'swarmmemo-gate-result' || typeof m.data?.token !== 'string') return;
      stop(); passed(m.data);
    }
    addEventListener('message', listen);
  }
  check.onclick = work;
  idButton.onclick = () => identity('');
  if (invisible && form) {
    // Start early, when the visitor first focuses the form; send it once
    // the token is in. A refusal opens the dialog only on submit.
    form.addEventListener('focusin', () => { if (state === 'idle' && me.isConnected) work(); });
    form.addEventListener('submit', event => {
      if (!me.isConnected || state === 'passed' && expires - Date.now() > 20000) return;
      event.preventDefault(); event.stopImmediatePropagation();
      want = event.submitter || form;
      if (state === 'passed') state = 'idle';
      if (state === 'refused') show();
      else if (state !== 'busy') work();
    }, true);
  }
  // Status first: a site's fail-open or fail-closed choice applies when the
  // gate cannot answer at all.
  fetch(SM + '/v1/gate/status', {credentials: 'omit', referrerPolicy: 'no-referrer', cache: 'no-store'})
    .then(r => r.json()).then(s => { if (s?.ok !== true) unavailable(); else if (!status.textContent && !invisible) say(min > 0 ? 'This site asks for standing, shown by your SwarmMemo key.' : 'Proof of work in your browser, or your SwarmMemo identity.'); })
    .catch(unavailable);
})();
