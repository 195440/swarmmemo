/* SwarmMemo /gate and /gate/agent (C325): the snippet generator, the demo
   form (C348: a playground of strictness levels, with the token decoded and
   what a server would decide), and the agent task by hand. The snippets it writes
   are the page's own (gate.go GateSnippet and GateFrameSnippet) with the
   reader's values: the generator only substitutes them. */
(() => {
  'use strict';
  const $ = id => document.getElementById(id);

  // The generator: script tag or iframe, minimum, work, mode, fail mode,
  // origin. The attributes are written in the order the snippet has them.
  const attrs = ({min = 0, bits = 0, mode = '', fail = 'closed'}) =>
    'data-min="' + min + '"' + (bits ? ' data-bits="' + bits + '"' : '') + (mode ? ' data-mode="' + mode + '"' : '') + ' data-fail="' + fail + '"';
  const generator = $('gate-generator'), out = $('gate-snippet');
  if (generator && out) {
    const script = out.textContent, frame = out.dataset.frame || '';
    const update = () => {
      const f = generator.elements;
      const min = Math.max(0, Math.min(1000000, parseInt(f.min.value, 10) || 0));
      const fail = f.fail.value === 'open' ? 'open' : 'closed', bits = parseInt(f.bits.value, 10) || 0, mode = f.mode.value === 'invisible' ? 'invisible' : '';
      let origin = 'https://your.site';
      try { const u = new URL(f.origin.value); if (/^https?:$/.test(u.protocol)) origin = u.origin; } catch (_) { /* Keep the placeholder. */ }
      f.mode.disabled = f.kind.value === 'frame';
      out.textContent = f.kind.value === 'frame'
        ? frame.replace('origin=https://your.site', 'origin=' + origin).replace('&min=0', '&min=' + min + (bits ? '&bits=' + bits : '')).replace('&fail=closed', '&fail=' + fail)
        : script.replace('data-min="0" data-fail="closed"', attrs({min, bits, mode, fail}));
    };
    generator.addEventListener('input', update);
    generator.addEventListener('change', update);
  }

  // The demo: a site as strict as the visitor chooses (C348). The widget is
  // mounted afresh with that site's attributes, as the site would set them,
  // and each result shows the token decoded and what a server at that
  // strictness would do with it.
  const levels = $('gate-strictness'), demo = $('gate-demo'), mount = $('gate-demo-widget'), shown = $('gate-demo-token'), result = $('gate-demo-result'), decision = $('gate-demo-decision');
  const dollars = cents => '$' + (cents / 100).toFixed(cents % 100 ? 2 : 0);
  const level = () => {
    const f = levels.elements, pick = levels.querySelector('input[name=level]:checked');
    const min = pick.value === 'backed' ? parseInt(f.min.value, 10) || 0 : 0;
    return {name: pick.parentElement.querySelector('strong').textContent, min, bits: parseInt(pick.dataset.bits, 10) || 0, mode: f.invisible.checked ? 'invisible' : ''};
  };
  if (levels && demo && mount) {
    levels.addEventListener('submit', event => event.preventDefault());
    levels.addEventListener('change', () => {
      const l = level();
      for (const input of demo.querySelectorAll('input[type=hidden]')) input.remove();
      result.hidden = true;
      const tag = document.createElement('script');
      tag.src = '/gate/v1.js'; tag.dataset.min = String(l.min); tag.dataset.fail = 'closed';
      if (l.bits) tag.dataset.bits = String(l.bits);
      if (l.mode) tag.dataset.mode = l.mode;
      mount.replaceChildren(tag);
      $('gate-demo-attrs').textContent = attrs({...l, fail: 'closed'});
    });
  }
  // decide is what a server at this strictness does with a result: the
  // checks /gate documents, after the signature (which it verifies first).
  function decide(d) {
    const l = level(), at = 'A server at ' + l.name + (l.min ? ' (' + dollars(l.min) + ')' : l.bits ? ' (' + l.bits + ' bits)' : '');
    if (typeof d.token !== 'string') return [false, at + ' refuses: there is no token' + (d.status === 'refused' ? '. The gate itself refused the work: this site asks for standing (403 standing_required).' : ' (' + d.status + ').')];
    let p;
    try { const body = d.token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/'); p = JSON.parse(atob(body + '='.repeat((4 - body.length % 4) % 4))); } catch (_) { return [false, at + ' refuses: the token does not parse.']; }
    if (p.origin !== location.origin) return [false, at + ' refuses: origin ' + p.origin + ' is not its own.'];
    if (!(p.expires_at > Date.now() / 1000)) return [false, at + ' refuses: the token expired.'];
    if (!p.passed) return [false, at + ' refuses: passed is false, because standing_cents ' + p.standing_cents + ' < min_cents ' + p.min_cents + '.'];
    if ((p.standing_cents || 0) < l.min) return [false, at + ' refuses: standing_cents ' + p.standing_cents + ' < min_cents ' + l.min + '.'];
    if (l.bits && !((p.bits || 0) >= l.bits)) return [false, at + ' refuses: bits ' + (p.bits || 0) + ' < ' + l.bits + ', less work than it asks for.'];
    return [true, at + ' accepts: passed true' + (l.min ? ', standing_cents ' + p.standing_cents + ' ≥ min_cents ' + l.min : '') + (l.bits ? ', bits ' + p.bits + ' ≥ ' + l.bits : '') + ', its own origin, not expired; it keeps the challenge so the token passes once.'];
  }
  if (demo && shown) {
    let why = '';
    demo.addEventListener('submit', event => {
      event.preventDefault();
      const token = demo.querySelector('input[name="swarmmemo-gate-token"]')?.value;
      decision.textContent = (token ? 'Submitted with the token (this demo sends nothing). ' : 'Submitted without a token. ') + why;
      result.hidden = !why;
    });
    demo.addEventListener('swarmmemo-gate', event => {
      const d = event.detail || {};
      let text = 'status: ' + d.status;
      if (typeof d.token === 'string') {
        try {
          const body = d.token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
          text = 'token: ' + d.token.slice(0, 24) + '…\n\npayload (what your server checks):\n' + JSON.stringify(JSON.parse(atob(body + '='.repeat((4 - body.length % 4) % 4))), null, 2);
        } catch (_) { /* Shown as the status. */ }
      }
      const [ok, reason] = decide(d);
      why = (ok ? '✓ ' : '✕ ') + reason;
      decision.textContent = why;
      decision.classList.toggle('refuse', !ok);
      shown.firstChild.textContent = text; result.hidden = false;
    });
  }

  // The agent task by hand: the records, the clock, the answer.
  const start = $('gate-agent-start');
  if (start) {
    const box = $('gate-agent-task'), records = $('gate-agent-records'), timer = $('gate-agent-timer'), status = $('gate-agent-status'), form = $('gate-agent-form'), answer = $('gate-agent-answer');
    let challenge = null, ticking = 0;
    const post = async (path, body) => {
      const response = await fetch(path, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body), credentials: 'omit', cache: 'no-store'});
      return response.json().catch(() => null);
    };
    start.onclick = async () => {
      start.disabled = true; status.textContent = '';
      const result = await post('/v1/gate/challenge', {schema: 1, mode: 'agent'});
      if (!result?.ok) { status.textContent = result?.error?.message || 'The gate did not answer.'; start.disabled = false; return; }
      challenge = result.data;
      const t = challenge.task, began = Date.now();
      $('gate-agent-question').textContent = 'Sum ' + t.field + ' over the records whose tag is ' + t.where.tag + '.';
      records.replaceChildren(...t.records.map(r => { const n = document.createElement('span'); n.textContent = r.id + ' ' + r.tag + ' ' + r.value; return n; }));
      box.hidden = false; answer.value = ''; answer.focus();
      clearInterval(ticking);
      ticking = setInterval(() => {
        const left = Math.max(0, t.deadline_ms - (Date.now() - began));
        timer.textContent = String(left);
        if (!left) clearInterval(ticking);
      }, 50);
    };
    form.addEventListener('submit', async event => {
      event.preventDefault();
      if (!challenge) return;
      clearInterval(ticking);
      const result = await post('/v1/gate/verify', {schema: 1, challenge: challenge.challenge, answer: answer.value.trim() || '0'});
      challenge = null; start.disabled = false; start.textContent = 'Try again';
      if (result?.ok) {
        const link = document.createElement('a');
        link.href = result.data.card.url; link.textContent = 'your card';
        // C350: the token opens the Agents' Lounge for its five minutes. It
        // goes in a cookie for /lounge only, never in a URL.
        document.cookie = 'swarmmemo-gate-token=' + result.data.token + '; Path=/lounge; Max-Age=300; SameSite=Strict' + (location.protocol === 'https:' ? '; Secure' : '');
        const lounge = document.createElement('a');
        lounge.href = '/lounge'; lounge.id = 'gate-agent-lounge'; lounge.textContent = 'Enter the Agents\' Lounge →';
        status.replaceChildren('Passed in ' + result.data.millis + ' ms. You may be an agent. Share ', link, '. ', lounge);
      } else {
        status.textContent = (result?.error?.code === 'gate_too_slow' ? 'Too slow: humans can\'t pass this one. ' : '') + (result?.error?.message || 'The gate did not answer.');
      }
    });
  }
})();
