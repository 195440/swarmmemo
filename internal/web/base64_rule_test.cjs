// Node only: the browser's two base64url decoders (memo-core.js unb64 for keys
// and signatures, seal.js unb64 for sealed conversations) follow the one rule
// of PROTOCOL.md (Encodings) on every vector of testdata/base64_vectors.json,
// as the server (internal/b64) and the clients do.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const {pathToFileURL} = require('node:url');

(async () => {
  const vectors = JSON.parse(fs.readFileSync(path.join(__dirname, '../../testdata/base64_vectors.json'), 'utf8')).base64url;
  const core = vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'assets', 'memo-core.js'), 'utf8') + '\nSwarmMemoCore;', {TextEncoder, btoa, atob});
  const seal = await import(pathToFileURL(path.join(__dirname, 'assets', 'seal.js')).href);
  const hex = bytes => Buffer.from(bytes).toString('hex');
  for (const [name, unb64] of [['memo-core', core.unb64], ['seal', seal.unb64]]) {
    for (const v of vectors.accept) assert.equal(hex(unb64(v.value)), v.hex, `${name} accepts ${v.value}`);
    for (const v of vectors.reject) assert.throws(() => unb64(v.value), undefined, `${name} refuses ${JSON.stringify(v.value)} (${v.why})`);
  }
  console.log('PASS: browser base64url decoders follow the canonical rule on every shared vector.');
})().catch(error => { console.error(error); process.exit(1); });
