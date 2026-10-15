/* SwarmMemo /gate/pass (C325): the SwarmMemo Gate's first-party window. A
   site's gate opens it to pass as the visitor's SwarmMemo identity: the key
   this browser keeps on swarmmemo.com signs gate.verify here, so it never
   leaves this origin, and the token goes back by postMessage to the opener's
   origin only (via=frame: to the gate's own frame, which answers its page).
   Allow is offered only after the opener proves that origin (it answers this
   window's ready message from it). With no key here, the visitor passes with
   proof of work, and may keep a free identity: an explicit box, unticked,
   which makes a key and registers it (ref=gate) before it signs.
   C348 adds the ways through for a visitor a site refused (way=…): earn
   standing with the pow proof kind's work (gate-earn.js), planned from
   /v1/gate/status before it starts and honest about what it cannot reach,
   or link a wallet to the key (standing.challenge kind wallet, personal_sign,
   identity.link). Both count at the next nightly trust run; the window then
   offers the pass as the key, which states the standing as it is now.
   Loaded after memo-core.js, browser-key.js and gate-pow.js (and
   gate-earn.js for way=earn); never framed. */
(() => {
  'use strict';
  const $ = id => document.getElementById(id);
  const allow = $('gate-pass-allow'), cancel = $('gate-pass-cancel'), line = $('gate-pass-status');
  if (!allow) return;
  const opened = performance.now(), ARM_MS = 800;
  const d = document.body.dataset, origin = d.origin, service = d.service || 'swarmmemo.com';
  const min = parseInt(d.min || '0', 10) || 0, bits = parseInt(d.bits || '0', 10) || 0, nonce = d.nonce || '', frame = d.via === 'frame';
  const way = d.way === 'earn' || d.way === 'wallet' ? d.way : '';
  const opener = window.opener, replyTo = frame ? location.origin : origin;
  const say = (text, error = false) => { line.textContent = text; line.classList.toggle('error', error); };
  const tell = message => { try { opener?.postMessage(message, replyTo); } catch (_) { /* The opener left. */ } };
  const dollars = cents => '$' + (cents / 100).toFixed(cents % 100 ? 2 : 0);
  // A fraction of a cent, said as one: 0.0003¢, or less than 0.0001¢.
  const centsText = c => c >= 100 ? dollars(Math.floor(c)) : c >= 1 ? Math.floor(c) + '¢' : c >= 1e-4 ? c.toFixed(Math.ceil(-Math.log10(c))) + '¢' : c > 0 ? 'less than 0.0001¢' : '0¢';
  function timeText(s) {
    if (!(s < Infinity)) return 'forever';
    if (s < 90) return Math.max(1, Math.round(s)) + ' s';
    if (s < 5400) return Math.floor(s / 60) + ' min' + (s < 600 && Math.round(s % 60) ? ' ' + Math.round(s % 60) + ' s' : '');
    if (s < 172800) return Math.round(s / 3600) + ' hours';
    if (s < 2 * 31557600) return Math.round(s / 86400) + ' days';
    return Math.round(s / 31557600).toLocaleString('en') + ' years';
  }
  cancel.onclick = () => { tell({type: 'swarmmemo-gate-cancel'}); window.close(); };

  async function post(path, body) {
    const response = await fetch(path, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body), credentials: 'omit', cache: 'no-store'});
    const result = await response.json().catch(() => null);
    if (!response.ok || result?.ok !== true) throw Error(result?.error?.message || 'SwarmMemo could not answer. Try again.');
    return result;
  }
  let key = SwarmMemoKeys.stored(service);
  const signed = async (operation, data) => (await post('/v1/command', await SwarmMemoKeys.sign({operation, data: JSON.stringify({schema: 1, ...data}), request_id: SwarmMemoKeys.uuid()}, key, service))).data || {};
  function solve(prefix, n) {
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
        w.onerror = () => { w.terminate(); SwarmMemoGatePow.solve(prefix, n, progress).then(resolve, reject); };
        w.postMessage({prefix, bits: n});
      });
    } catch (_) { return SwarmMemoGatePow.solve(prefix, n, progress); }
  }

  // What standing counts, from /v1/gate/status (C348).
  let standing = null;
  const countsAt = () => standing?.counts_at ? new Date(standing.counts_at * 1000).toISOString().slice(11, 16) + ' UTC' : 'midnight UTC';
  async function readStanding() {
    try { standing = (await (await fetch('/v1/gate/status', {credentials: 'omit', cache: 'no-store'})).json()).standing || null; } catch (_) { standing = null; }
  }

  // Earn: the plan, then the work, spread over a few threads.
  const EARN_SECONDS = 180, ANYWAY_SECONDS = 120;
  const threads = Math.max(1, Math.min(8, Math.floor((navigator.hardwareConcurrency || 2) / 2)));
  let plan = null, rate = 0, done = false;
  const workers = n => { try { return Array.from({length: n}, () => new Worker('/assets/gate-earn.js')); } catch (_) { return []; } };
  async function measure() {
    const pow = standing?.pow, out = $('gate-earn-plan');
    if (!standing?.counted || !pow) { out.textContent = 'This service counts no standing, so work here would add none. Choose another way.'; return false; }
    const ws = workers(threads);
    const counts = ws.length ? await Promise.all(ws.map(w => new Promise(resolve => { w.onmessage = e => { w.terminate(); resolve(e.data?.bench || 0); }; w.onerror = () => resolve(0); w.postMessage({bench: 800}); })))
      : [SwarmMemoGateEarn.bench(800)];
    rate = Math.max(1, counts.reduce((a, b) => a + b, 0) / 0.8);
    plan = SwarmMemoGateEarn.plan(pow, min, rate, EARN_SECONDS, pow.per_day);
    const speed = 'This browser does about ' + (rate / 1e6).toFixed(1) + ' million hashes a second.';
    if (plan.reach) {
      out.textContent = speed + ' About ' + timeText(plan.seconds) + ' of work reaches ' + dollars(min) + ' (it adds ' + centsText(plan.cents) + ' at today\'s prices). It counts at the next trust run, after ' + countsAt() + '; then this site lets your key in.';
      allow.textContent = 'Start (about ' + timeText(plan.seconds) + ')';
      return true;
    }
    plan = SwarmMemoGateEarn.plan(pow, 0, rate, ANYWAY_SECONDS, pow.per_day);
    const exact = pow.ceiling_cents * plan.units / (plan.units + pow.ceiling_cents * pow.units_per_cent);
    const need = SwarmMemoGateEarn.unitsFor(pow, min);
    const why = min >= pow.ceiling_cents
      ? 'Proof of work tops out at ' + dollars(pow.ceiling_cents) + ', below this site\'s ' + dollars(min) + ', however long it runs.'
      : 'Reaching ' + dollars(min) + ' would take this browser about ' + timeText(need * pow.unit_hashes / rate) + ': work is priced at what a rented GPU pays for the same hashes (about ' + (pow.unit_hashes * pow.units_per_cent / 1e12).toPrecision(2) + ' trillion hashes a cent), up to ' + dollars(pow.ceiling_cents) + '.';
    out.textContent = speed + ' ' + why + ' Two minutes here add ' + (exact >= 1e-4 ? 'about ' : '') + centsText(exact) + '. Faster: pass as a SwarmMemo identity that has standing, or link a wallet (below).';
    allow.textContent = 'Work 2 minutes anyway';
    return true;
  }
  // One solution over every thread: n ≡ i (mod threads) on thread i.
  function search(prefix, n, report) {
    const ws = workers(threads);
    if (!ws.length) return SwarmMemoGateEarn.search(prefix, n, 0, 1, report);
    return new Promise((resolve, reject) => {
      const stop = () => ws.forEach(w => w.terminate());
      ws.forEach((w, i) => {
        w.onmessage = e => {
          const m = e.data || {};
          if (m.hashes) report(m.hashes);
          if (m.solution) { stop(); resolve(m.solution); }
          if (m.error) { stop(); reject(Error(m.error)); }
        };
        w.onerror = () => { stop(); reject(Error('The work stopped. Try again.')); };
        w.postMessage({prefix, bits: n, start: i, stride: ws.length});
      });
    });
  }
  async function earn() {
    const run = $('gate-earn-run'), bar = $('gate-earn-progress'), eta = $('gate-earn-eta');
    run.hidden = false;
    const per = 2 ** plan.bits, began = performance.now();
    let hashes = 0, result = {};
    for (let i = 0; i < plan.count; i++) {
      const ch = await signed('standing.challenge', {kind: 'pow', bits: plan.bits});
      let here = 0;
      const solution = await search(ch.prefix, plan.bits, h => {
        hashes += h; here += h;
        const p = (i + Math.min(0.95, here / per)) / plan.count, live = hashes / Math.max(0.001, (performance.now() - began) / 1000);
        bar.value = Math.round(p * 1000);
        eta.textContent = Math.round(p * 100) + '% · about ' + timeText(((plan.count - i) * per - here) / live) + ' left · proof ' + (i + 1) + ' of ' + plan.count + ' · ' + (live / 1e6).toFixed(1) + ' M hashes/s';
      });
      result = await signed('standing.work', {nonce: ch.nonce, solution});
      if (plan.reach && result.adds_cents >= min) break;
    }
    bar.value = 1000;
    const adds = result.adds_cents || 0;
    eta.textContent = 'Done in ' + timeText((performance.now() - began) / 1000) + '.';
    $('gate-earn-plan').textContent = 'Your key\'s proof of work is now ' + Number(result.total_work_units || 0).toLocaleString('en') + ' units, worth ' + centsText(adds) + ' at today\'s prices' +
      (adds >= min ? ': enough for this site once the next trust run counts it, after ' + countsAt() + '.' : ', short of this site\'s ' + dollars(min) + '. It counts at the next trust run, after ' + countsAt() + '.') +
      ' Pass as yourself now and the site sees your standing as it is today.';
  }

  // Wallet: link it to the key, so it counts as standing.
  const utf8Hex = text => '0x' + Array.from(new TextEncoder().encode(text), b => b.toString(16).padStart(2, '0')).join('');
  async function linkWallet() {
    const wallet = window.ethereum;
    if (!wallet?.request) throw Error('No browser wallet in this window. Link one in Me by pasting its signature, or choose another way.');
    say('Asking your wallet…');
    const value = (await wallet.request({method: 'eth_requestAccounts'}))[0] || '';
    const ch = await signed('standing.challenge', {kind: 'wallet', value});
    say('Sign the message in your wallet: it names this key and costs nothing.');
    const proof = await wallet.request({method: 'personal_sign', params: [utf8Hex(ch.message), ch.value]});
    await signed('identity.link', {kind: 'wallet', value: ch.value, proof, nonce: ch.nonce});
    return ch.value;
  }

  async function pass() {
    say('Asking for a challenge…');
    const body = {schema: 1, origin};
    if (min) body.min = min;
    if (nonce) body.nonce = nonce;
    if (bits) body.bits = bits;
    const ch = (await post('/v1/gate/challenge', body)).data;
    const data = {schema: 1, challenge: ch.challenge};
    // Work beside the key unless the site asks for standing: a key with
    // none yet passes only with it.
    if (ch.pow && !min) data.solution = await solve(ch.pow.prefix, ch.pow.bits);
    say('Checking…');
    const verify = key ? await SwarmMemoKeys.sign({operation: 'gate.verify', data: JSON.stringify(data)}, key, service) : data;
    const result = (await post('/v1/gate/verify', verify)).data;
    tell({type: 'swarmmemo-gate-result', data: result});
    say(result.passed ? 'Verified. You can close this window.' : 'Your standing is ' + dollars(result.standing_cents) + '; this site asks for ' + dollars(min) + '.' + (way ? ' It counts what you added at the next trust run, after ' + countsAt() + '.' : ' Raise it in Me.'), !result.passed);
    if (result.passed) setTimeout(() => window.close(), 900);
  }

  async function main() {
    if (!opener) throw Error('Open this from a site\'s SwarmMemo Gate.');
    const keep = $('gate-pass-keep'), keepRow = $('gate-pass-keep-row');
    let armed = false, ready = true;
    const arm = () => { allow.disabled = !armed || !ready || Boolean(way && !key && !keep.checked); };
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
      if (!way) {
        allow.textContent = min > 0 ? 'Make a free key and continue' : 'Verify';
        if (min > 0) keep.checked = true, keep.disabled = true;
      }
      // A way through needs a key: the box stays the visitor's explicit choice.
      keep.addEventListener('change', arm);
    }
    if (min > 0) {
      // The other ways through, in this same window (its opener stays).
      const more = document.createElement('p');
      more.className = 'small';
      more.id = 'gate-pass-ways';
      more.append(way ? 'Other ways: ' : 'Not enough standing? ');
      const ways = [['', 'Pass as my identity'], ['wallet', 'Link a wallet'], ['earn', 'Earn standing in your browser']].filter(([w]) => w !== way);
      ways.forEach(([w, text], i) => {
        const a = document.createElement('a'), q = new URLSearchParams(location.search);
        if (w) q.set('way', w); else q.delete('way');
        a.href = '?' + q; a.textContent = text; more.append(a, i ? '.' : ' · ');
      });
      allow.parentElement.after(more);
    }
    if (way) {
      await readStanding();
      if (way === 'wallet') {
        const w = standing?.wallet_cents || 0;
        $('gate-wallet-cents').textContent = standing?.counted ? 'at least ' + dollars(w) + ', more with the address\'s history' + (w >= min ? ', enough for this site' : ', not enough alone for this site\'s ' + dollars(min)) : 'none: this service counts no standing';
      } else ready = await measure();
    }
    // The opener proves its origin: only a window there can answer. Allow
    // arms no sooner than ARM_MS after this window opened, so a click the
    // opener aimed at its own page (a double click as the window appears)
    // never lands on it.
    addEventListener('message', event => {
      if (event.source !== opener || event.origin !== replyTo || event.data?.type !== 'swarmmemo-gate-hello') return;
      setTimeout(() => { armed = true; arm(); if (!line.classList.contains('error')) say(''); }, Math.max(0, opened + ARM_MS - performance.now()));
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
          $('gate-pass-nokey').hidden = keepRow.hidden = true;
          $('gate-pass-who').textContent = key.fingerprint.slice(0, 12);
          $('gate-pass-shares').hidden = false;
          say('');
        }
        if (way && !key) throw Error('Tick the box to keep a free identity: standing belongs to a key.');
        if (way && !done) {
          if (way === 'earn') { allow.textContent = 'Working…'; await earn(); }
          else {
            const address = await linkWallet();
            say('Wallet ' + address.slice(0, 6) + '…' + address.slice(-4) + ' linked to your key. It counts at the next trust run, after ' + countsAt() + '.');
          }
          done = true;
          allow.textContent = 'Pass as yourself';
          allow.disabled = false;
          if (way === 'earn') say('');
          return;
        }
        await pass();
      } catch (error) { say(error.message, true); allow.disabled = false; }
    };
  }
  main().catch(error => say(error.message, true));
})();
