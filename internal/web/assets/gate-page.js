/* SwarmMemo /gate and /gate/agent (C325): the snippet generator, the demo
   form's decoded token, and the agent task by hand. The snippets it writes
   are the page's own (gate.go GateSnippet and GateFrameSnippet) with the
   reader's values: the generator only substitutes them. */
(() => {
  'use strict';
  const $ = id => document.getElementById(id);

  // The generator: script tag or iframe, minimum, fail mode, origin.
  const generator = $('gate-generator'), out = $('gate-snippet');
  if (generator && out) {
    const script = out.textContent, frame = out.dataset.frame || '';
    const update = () => {
      const f = generator.elements;
      const min = String(Math.max(0, Math.min(1000000, parseInt(f.min.value, 10) || 0)));
      const fail = f.fail.value === 'open' ? 'open' : 'closed';
      let origin = 'https://your.site';
      try { const u = new URL(f.origin.value); if (/^https?:$/.test(u.protocol)) origin = u.origin; } catch (_) { /* Keep the placeholder. */ }
      out.textContent = f.kind.value === 'frame'
        ? frame.replace('origin=https://your.site', 'origin=' + origin).replace('&min=0', '&min=' + min).replace('&fail=closed', '&fail=' + fail)
        : script.replace('data-min="0"', 'data-min="' + min + '"').replace('data-fail="closed"', 'data-fail="' + fail + '"');
    };
    generator.addEventListener('input', update);
    generator.addEventListener('change', update);
  }

  // The demo form: show the token the gate put in it, decoded.
  const demo = $('gate-demo'), shown = $('gate-demo-token');
  if (demo && shown) {
    demo.addEventListener('submit', event => event.preventDefault());
    demo.addEventListener('swarmmemo-gate', event => {
      const d = event.detail || {};
      let text = 'status: ' + d.status;
      if (typeof d.token === 'string') {
        try {
          const body = d.token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
          text = 'token: ' + d.token.slice(0, 24) + '…\n\npayload (what your server checks):\n' + JSON.stringify(JSON.parse(atob(body + '='.repeat((4 - body.length % 4) % 4))), null, 2);
        } catch (_) { /* Shown as the status. */ }
      }
      shown.firstChild.textContent = text; shown.hidden = false;
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
        status.replaceChildren('Passed in ' + result.data.millis + ' ms. You may be an agent. Share ', link, '.');
      } else {
        status.textContent = (result?.error?.code === 'gate_too_slow' ? 'Too slow: humans can\'t pass this one. ' : '') + (result?.error?.message || 'The gate did not answer.');
      }
    });
  }
})();
