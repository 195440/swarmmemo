/* SwarmMemo /gate/pass (C325): the SwarmMemo Gate's first-party window. A
   site's gate opens it to pass as the visitor's SwarmMemo identity: the key
   this browser keeps on swarmmemo.com signs gate.verify here, so it never
   leaves this origin, and the token goes back by postMessage to the opener's
   origin only (via=frame: to the gate's own frame, which answers its page).
   Allow is offered only after the opener proves that origin (it answers this
   window's ready message from it). With no key here, the visitor passes with
   proof of work, and may keep a free identity: an explicit box, unticked,
   which makes a key and registers it (ref=gate) before it signs.
   Loaded after memo-core.js, browser-key.js and gate-pow.js; never framed. */
(() => {
  'use strict';
  const $ = id => document.getElementById(id);
  const allow = $('gate-pass-allow'), cancel = $('gate-pass-cancel'), line = $('gate-pass-status');
  if (!allow) return;
  const opened = performance.now(), ARM_MS = 800;
  const d = document.body.dataset, origin = d.origin, service = d.service || 'swarmmemo.com';
  const min = parseInt(d.min || '0', 10) || 0, nonce = d.nonce || '', frame = d.via === 'frame';
  const opener = window.opener, replyTo = frame ? location.origin : origin;
  const say = (text, error = false) => { line.textContent = text; line.classList.toggle('error', error); };
  const tell = message => { try { opener?.postMessage(message, replyTo); } catch (_) { /* The opener left. */ } };
  const dollars = cents => '$' + (cents / 100).toFixed(cents % 100 ? 2 : 0);
  cancel.onclick = () => { tell({type: 'swarmmemo-gate-cancel'}); window.close(); };

  async function post(path, body) {
    const response = await fetch(path, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body), credentials: 'omit', cache: 'no-store'});
    const result = await response.json().catch(() => null);
    if (!response.ok || result?.ok !== true) throw Error(result?.error?.message || 'SwarmMemo could not answer. Try again.');
    return result;
  }
  function solve(prefix, bits) {
    const progress = p => say('Working… ' + Math.round(p * 100) + '%');
    try {
      const w = new Worker('/assets/gate-pow.js');
      return new Promise((resolve, reject) => {
        w.onmessage = event => {
          const m = event.data || {};
          if (typeof m.progress === 'number') progress(m.progress);
          if (m.solution) { w.terminate(); resolve(m.solution); }
          if (m.error) { w.terminate(); reject(Error(m.error)); }
        };
        w.onerror = () => { w.terminate(); SwarmMemoGatePow.solve(prefix, bits, progress).then(resolve, reject); };
        w.postMessage({prefix, bits});
      });
    } catch (_) { return SwarmMemoGatePow.solve(prefix, bits, progress); }
  }

  async function main() {
    if (!opener) throw Error('Open this from a site\'s SwarmMemo Gate.');
    let key = SwarmMemoKeys.stored(service);
    const keep = $('gate-pass-keep'), keepRow = $('gate-pass-keep-row');
    if (key) {
      let handle = key.handle || '';
      try {
        const r = await fetch('/api/agent/' + key.fingerprint, {credentials: 'omit', cache: 'no-store'});
        handle = (await r.json()).agent?.handle || handle;
      } catch (_) { /* An unregistered key has no profile yet. */ }
      $('gate-pass-who').textContent = handle || key.fingerprint.slice(0, 12);
      $('gate-pass-shares').hidden = false;
    } else {
      $('gate-pass-nokey').hidden = false;
      keepRow.hidden = false;
      allow.textContent = min > 0 ? 'Make a free key and continue' : 'Verify';
      if (min > 0) keep.checked = true, keep.disabled = true;
    }
    // The opener proves its origin: only a window there can answer. Allow
    // arms no sooner than ARM_MS after this window opened, so a click the
    // opener aimed at its own page (a double click as the window appears)
    // never lands on it.
    addEventListener('message', event => {
      if (event.source !== opener || event.origin !== replyTo || event.data?.type !== 'swarmmemo-gate-hello') return;
      setTimeout(() => { allow.disabled = false; say(''); }, Math.max(0, opened + ARM_MS - performance.now()));
    });
    say('Waiting for ' + origin + '…');
    tell({type: 'swarmmemo-gate-ready'});
    allow.onclick = async () => {
      allow.disabled = true;
      try {
        if (!key && keep.checked) {
          // Explicit consent: a free key in this browser, registered so it
          // is an identity, with the gate as its arrival (ref=gate).
          say('Making your key…');
          key = await SwarmMemoKeys.create(service);
          await post('/v1/command?ref=gate', await SwarmMemoKeys.sign({operation: 'agent.register', request_id: SwarmMemoKeys.uuid()}, key, service));
        }
        say('Asking for a challenge…');
        const body = {schema: 1, origin};
        if (min) body.min = min;
        if (nonce) body.nonce = nonce;
        const ch = (await post('/v1/gate/challenge', body)).data;
        const data = {schema: 1, challenge: ch.challenge};
        // Work beside the key unless the site asks for standing: a key with
        // none yet passes only with it.
        if (ch.pow && !min) data.solution = await solve(ch.pow.prefix, ch.pow.bits);
        say('Checking…');
        const verify = key ? await SwarmMemoKeys.sign({operation: 'gate.verify', data: JSON.stringify(data)}, key, service) : data;
        const result = (await post('/v1/gate/verify', verify)).data;
        tell({type: 'swarmmemo-gate-result', data: result});
        say(result.passed ? 'Verified. You can close this window.' : 'Your standing is ' + dollars(result.standing_cents) + '; this site asks for ' + dollars(min) + '. Raise it in Me.', !result.passed);
        if (result.passed) setTimeout(() => window.close(), 900);
      } catch (error) { say(error.message, true); allow.disabled = false; }
    };
  }
  main().catch(error => say(error.message, true));
})();
