/* SwarmMemo Gate proof of work (C325): find a solution such that
   SHA-256(prefix + solution) has at least `bits` leading zero bits, the same
   rule the server checks (internal/gate CheckWork). One small SHA-256 for
   one-block messages, counting solutions in base 36. Runs as a Worker
   (/assets/gate-pow.js: post {prefix, bits}, get {progress} then
   {solution}), or on the page in slices that yield, where no Worker can
   start. The gate's embed (/gate/v1.js) carries this file twice: as code,
   and as the source of a Blob worker. */
const SwarmMemoGatePow = (() => {
  'use strict';
  const K = new Uint32Array([0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2]);
  const W = new Uint32Array(64), block = new Uint8Array(64);
  // The first 64 bits of SHA-256(bytes[0..n)), n <= 55: one block.
  function head(bytes, n) {
    block.fill(0); block.set(bytes.subarray(0, n)); block[n] = 0x80; block[62] = (n * 8) >>> 8; block[63] = (n * 8) & 255;
    for (let i = 0; i < 16; i++) W[i] = (block[4 * i] << 24) | (block[4 * i + 1] << 16) | (block[4 * i + 2] << 8) | block[4 * i + 3];
    for (let i = 16; i < 64; i++) {
      const x = W[i - 15], y = W[i - 2];
      W[i] = (W[i - 16] + (((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3)) + W[i - 7] + (((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10))) | 0;
    }
    let a = 0x6a09e667, b = 0xbb67ae85, c = 0x3c6ef372, d = 0xa54ff53a, e = 0x510e527f, f = 0x9b05688c, g = 0x1f83d9ab, h = 0x5be0cd19;
    for (let i = 0; i < 64; i++) {
      const t1 = (h + (((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7))) + ((e & f) ^ (~e & g)) + K[i] + W[i]) | 0;
      const t2 = ((((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10))) + ((a & b) ^ (a & c) ^ (b & c))) | 0;
      h = g; g = f; f = e; e = (d + t1) | 0; d = c; c = b; b = a; a = (t1 + t2) | 0;
    }
    return [(a + 0x6a09e667) >>> 0, (b + 0xbb67ae85) >>> 0];
  }
  const zeros = (hi, lo) => hi ? Math.clz32(hi) : 32 + Math.clz32(lo);
  // solve resolves to the first solution, counting 0, 1, … in base 36, and
  // calls progress(p) with the chance it would have been found by now.
  async function solve(prefix, bits, progress = () => {}) {
    if (!/^[0-9a-f]{32}$/.test(prefix) || !(bits >= 1 && bits <= 64)) throw Error('Bad proof-of-work challenge.');
    const bytes = new Uint8Array(64);
    for (let i = 0; i < prefix.length; i++) bytes[i] = prefix.charCodeAt(i);
    const expected = 2 ** bits;
    for (let n = 0; ;) {
      const until = Date.now() + 40;
      do {
        const s = n.toString(36);
        for (let i = 0; i < s.length; i++) bytes[prefix.length + i] = s.charCodeAt(i);
        const [hi, lo] = head(bytes, prefix.length + s.length);
        if (zeros(hi, lo) >= bits) { progress(1); return s; }
        n++;
      } while ((n & 1023) || Date.now() < until);
      progress(1 - Math.exp(-n / expected));
      await new Promise(resolve => setTimeout(resolve, 0));
    }
  }
  return Object.freeze({solve, head});
})();
if (typeof WorkerGlobalScope !== 'undefined' && self instanceof WorkerGlobalScope) {
  self.onmessage = event => {
    const {prefix, bits} = event.data || {};
    SwarmMemoGatePow.solve(prefix, bits, progress => self.postMessage({progress}))
      .then(solution => self.postMessage({solution}), error => self.postMessage({error: error.message}));
  };
}
