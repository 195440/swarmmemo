/* SwarmMemo Gate: earn standing in the browser (C348). The pow proof kind's
   work: standing.challenge {"kind":"pow","bits":B} answers a prefix, and a
   solution S with SHA-256(prefix + S) starting with B zero bits is sent with
   standing.work, which adds 2^(B-20) units to the key's pow root (the server
   checks it; internal/proofs Pow). The prefix is longer than one SHA-256
   block, so the blocks it fills are hashed once (the midstate) and each try
   hashes only the last one. As a Worker (/assets/gate-earn.js): post
   {prefix, bits, start, stride} to search n = start, start + stride, … in
   base 36 ({hashes} now and then, then {solution}), or {bench: ms} to count
   the hashes this thread does in that time. On the page it is the plan:
   what the work earns at the pow row's prices, read from /v1/gate/status. */
const SwarmMemoGateEarn = (() => {
  'use strict';
  const K = new Uint32Array([0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2]);
  const IV = [0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19];
  const W = new Int32Array(64);
  // compress runs one 64-byte block at off in bytes over the state in
  // (8 words) into out (which may be in).
  function compress(inp, bytes, off, out) {
    for (let i = 0; i < 16; i++) { const j = off + 4 * i; W[i] = (bytes[j] << 24) | (bytes[j + 1] << 16) | (bytes[j + 2] << 8) | bytes[j + 3]; }
    for (let i = 16; i < 64; i++) {
      const x = W[i - 15], y = W[i - 2];
      W[i] = (W[i - 16] + (((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3)) + W[i - 7] + (((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10))) | 0;
    }
    let a = inp[0], b = inp[1], c = inp[2], d = inp[3], e = inp[4], f = inp[5], g = inp[6], h = inp[7];
    for (let i = 0; i < 64; i++) {
      const t1 = (h + (((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7))) + ((e & f) ^ (~e & g)) + K[i] + W[i]) | 0;
      const t2 = ((((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10))) + ((a & b) ^ (a & c) ^ (b & c))) | 0;
      h = g; g = f; f = e; e = (d + t1) | 0; d = c; c = b; b = a; a = (t1 + t2) | 0;
    }
    out[0] = inp[0] + a; out[1] = inp[1] + b; out[2] = inp[2] + c; out[3] = inp[3] + d;
    out[4] = inp[4] + e; out[5] = inp[5] + f; out[6] = inp[6] + g; out[7] = inp[7] + h;
  }
  // hasher(prefix) is try(s): the leading zero bits of SHA-256(prefix + s).
  function hasher(prefix) {
    const p = new TextEncoder().encode(prefix), full = p.length - p.length % 64;
    const mid = Int32Array.from(IV), st = new Int32Array(8);
    for (let off = 0; off < full; off += 64) compress(mid, p, off, mid);
    const tail = p.subarray(full), last = new Uint8Array(128);
    return s => {
      const n = tail.length + s.length, blocks = n + 9 > 64 ? 2 : 1, bitsLen = (p.length + s.length) * 8;
      last.fill(0); last.set(tail);
      for (let i = 0; i < s.length; i++) last[tail.length + i] = s.charCodeAt(i);
      last[n] = 0x80;
      const end = blocks * 64;
      last[end - 4] = bitsLen >>> 24; last[end - 3] = (bitsLen >>> 16) & 255; last[end - 2] = (bitsLen >>> 8) & 255; last[end - 1] = bitsLen & 255;
      compress(mid, last, 0, st);
      if (blocks === 2) compress(st, last, 64, st);
      return st[0] ? Math.clz32(st[0]) : 32 + Math.clz32(st[1]);
    };
  }
  // search finds n ≡ start (mod stride) with try(n in base 36) ≥ bits,
  // reporting the hashes done every few thousand; stop() ends it.
  async function search(prefix, bits, start, stride, report = () => {}, stop = () => false) {
    if (typeof prefix !== 'string' || prefix.length > 512 || !(bits >= 1 && bits <= 52)) throw Error('Bad proof-of-work challenge.');
    const go = hasher(prefix);
    for (let n = start, done = 0; ;) {
      const until = Date.now() + 50;
      do {
        const s = n.toString(36);
        if (go(s) >= bits) { report(done + 1); return s; }
        n += stride; done++;
      } while ((done & 1023) || Date.now() < until);
      report(done); done = 0;
      await new Promise(resolve => setTimeout(resolve, 0));
      if (stop()) return '';
    }
  }
  // bench counts the tries in ms, after a short warm-up.
  function bench(ms) {
    const go = hasher('swarmmemo-pow:1:bench:' + '0'.repeat(96) + ':');
    let n = 0;
    for (const warm = Date.now() + 150; Date.now() < warm;) for (let i = 0; i < 1024; i++) go((n++).toString(36));
    const start = n, until = Date.now() + ms;
    while (Date.now() < until) for (let i = 0; i < 1024; i++) go((n++).toString(36));
    return n - start;
  }
  // The pow row's price: ceiling × v / (v + ceiling × units_per_cent),
  // rounded down (internal/trust AssessedContribution).
  const cents = (pow, units) => units > 0 ? Math.floor(pow.ceiling_cents * units / (units + pow.ceiling_cents * pow.units_per_cent)) : 0;
  // unitsFor is the least work that prices at target cents (Infinity at or
  // above the ceiling).
  function unitsFor(pow, target) {
    if (target <= 0) return 0;
    if (target >= pow.ceiling_cents) return Infinity;
    let v = Math.ceil(target * pow.ceiling_cents * pow.units_per_cent / (pow.ceiling_cents - target));
    while (cents(pow, v) < target) v++;
    while (v > 1 && cents(pow, v - 1) >= target) v--;
    return v;
  }
  // plan is how to reach target cents (above 0) at rate hashes a second
  // within seconds, or the work that much time does: solutions of bits
  // each, about ten seconds apiece, no more than the day allows.
  function plan(pow, target, rate, seconds, perDay) {
    const unit = pow.unit_hashes, need = target > 0 ? unitsFor(pow, target) : Infinity;
    const reach = need !== Infinity && need * unit / rate <= seconds;
    const units = reach ? need : Math.max(1, Math.floor(rate * seconds / unit));
    let bits = Math.max(pow.bits.minimum, Math.min(pow.bits.maximum, Math.floor(Math.log2(rate * 10)), 20 + Math.floor(Math.log2(units))));
    while (Math.ceil(units / 2 ** (bits - 20)) > Math.min(perDay, 40) && bits < pow.bits.maximum) bits++;
    const count = Math.max(1, Math.ceil(units / 2 ** (bits - 20)));
    return {reach, need, bits, count, units: count * 2 ** (bits - 20), seconds: count * 2 ** bits / rate, cents: cents(pow, count * 2 ** (bits - 20))};
  }
  return Object.freeze({search, bench, cents, unitsFor, plan, hasher});
})();
if (typeof WorkerGlobalScope !== 'undefined' && self instanceof WorkerGlobalScope) {
  let stopped = false;
  self.onmessage = event => {
    const m = event.data || {};
    if (m.stop) { stopped = true; return; }
    if (m.bench) { self.postMessage({bench: SwarmMemoGateEarn.bench(Math.min(5000, m.bench))}); return; }
    stopped = false;
    SwarmMemoGateEarn.search(m.prefix, m.bits, m.start || 0, m.stride || 1, hashes => self.postMessage({hashes}), () => stopped)
      .then(solution => self.postMessage({solution}), error => self.postMessage({error: error.message}));
  };
}
