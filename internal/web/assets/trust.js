// /trust: the one interactive, "what one judgement carries" (RFC 0021 §2).
// The figure carries the published parameters as data attributes (U, the
// coarse acts' score, the evidence weights, node_cap and the vote weight
// v(s)), and the text under it already states the default example, so the
// page reads the same without this script. A judgement above 50 is an edge
// of capacity U × (x − 50)/50 × evidence; an account keeps its own costs
// first, up to node_cap, and offers its edges what it holds above that or
// pass_share_ppm of what it keeps, whichever is more (trust.Params.OfferCents).
// Text is only ever set as textContent.
(() => {
  'use strict';
  const root = document.getElementById('tx');
  const fig = root && root.querySelector('[data-fig="edge"]');
  if (!fig) return;
  root.classList.add('tx-js');
  const num = (name, fallback) => {
    const v = Number(fig.dataset[name]);
    return Number.isFinite(v) && v >= 0 ? v : fallback;
  };
  const P = {
    unit: num('unit', 200),
    coarseX: num('coarseX', 75),
    nodeCap: num('nodeCap', 750),
    evidence: { opinion: num('evOpinion', 200000) / 1e6, observed: num('evObserved', 400000) / 1e6, work: num('evWork', 1000000) / 1e6 },
    v0: num('v0Ppm', 250000) / 1e6,
    cRef: num('cRef', 500) || 500,
    floor: num('vFloor', 50),
    pass: num('passSharePpm', 250000) / 1e6,
  };
  const classOf = { work: 'work', vouch: 'opinion', witness: 'observed', vote: 'opinion' };
  const names = { work: 'A rated accept', vouch: 'A vouch', witness: 'A verified witness', vote: 'An up vote' };
  const $ = (sel) => fig.querySelector(sel);
  const out = (name) => fig.querySelector('[data-out="' + name + '"]');
  const fmt = (n) => {
    if (!Number.isFinite(n) || n < 0) return '0';
    return Math.round(n).toLocaleString('en-US');
  };
  const voteWeight = (c) => (c < P.floor || c <= 0) ? 0 : P.v0 + (1 - P.v0) * Math.sqrt(Math.min(1, c / P.cRef));
  const setBar = (el, frac) => { if (el) el.style.width = (Math.max(0, Math.min(1, frac)) * 100).toFixed(1) + '%'; };

  function update() {
    const act = (fig.querySelector('[data-in="act"]:checked') || {}).value || 'work';
    const scoreIn = $('[data-in="score"]');
    const centsIn = $('[data-in="cents"]');
    const score = act === 'work' ? Math.min(100, Math.max(51, Math.round(Number(scoreIn.value) || 80))) : P.coarseX;
    const cents = Math.max(0, Number(centsIn.value) || 0);
    fig.querySelector('[data-for="score"]').hidden = act !== 'work';
    out('score').textContent = String(score);
    out('cents').textContent = fmt(cents) + ' cents';
    const edge = P.unit * (score - 50) / 50 * P.evidence[classOf[act]];
    const keep = Math.min(cents, P.nodeCap);
    const offer = Math.max(cents - keep, Math.floor(keep * P.pass));
    const passes = Math.min(edge, offer);
    const scale = Math.max(cents, 1);
    setBar(out('you-bar'), (keep - Math.max(0, passes - (cents - keep))) / scale);
    setBar(out('target-bar'), passes / scale);
    out('read').textContent = names[act] + (act === 'work' ? ' at ' + score : '') + ' is a ' + fmt(edge) + '¢ edge. You keep your own costs first, up to ' +
      fmt(P.nodeCap) + '¢, and pass on what you hold above that or ' + String(P.pass * 100) + '% of what you keep, whichever is more; of your ' + fmt(cents) + '¢, ' + fmt(passes) + '¢ can pass along it. Your vote weighs ' + voteWeight(cents).toFixed(2) + '.';
  }
  fig.addEventListener('input', update);
  fig.addEventListener('change', update);
  update();
})();
