/* SwarmMemo browser key: one copy of the key this browser keeps on
   swarmmemo.com (the slots app.js uses), for the first-party windows other
   sites open: /connect/embed (Sign in with SwarmMemo) and /gate/pass (the
   SwarmMemo Gate). Loaded after memo-core.js; adds one global. */
const SwarmMemoKeys = (() => {
  'use strict';
  const {b64, unb64, canonical} = SwarmMemoCore;
  const hex = bytes => Array.from(new Uint8Array(bytes), b => b.toString(16).padStart(2, '0')).join('');
  const uuid = () => crypto.randomUUID ? crypto.randomUUID() : hex(crypto.getRandomValues(new Uint8Array(16)));
  // The active key, or null: version 1, this service's, well formed.
  function stored(service) {
    let key = null;
    try { key = JSON.parse(localStorage.getItem('swarmmemo.identity.v1') || 'null'); } catch (_) { /* Reported as no key. */ }
    if (!key || key.version !== 1 || typeof key.public_key !== 'string' || typeof key.private_key !== 'string' || !/^[a-f0-9]{64}$/.test(key.fingerprint || '') || (key.service && key.service !== service)) return null;
    return key;
  }
  // A new key, kept like Me keeps one: the active key and the list of keys
  // in this browser, both read back to be sure they were stored.
  async function create(service) {
    const pair = await crypto.subtle.generateKey({name: 'Ed25519'}, true, ['sign', 'verify']);
    const raw = await crypto.subtle.exportKey('raw', pair.publicKey);
    const pkcs8 = new Uint8Array(await crypto.subtle.exportKey('pkcs8', pair.privateKey));
    const made = {version: 1, service, public_key: b64(raw), private_key: b64(pkcs8.slice(-32)), fingerprint: hex(await crypto.subtle.digest('SHA-256', raw)), handle: ''};
    const encoded = JSON.stringify(made);
    let list = [];
    try { const parsed = JSON.parse(localStorage.getItem('swarmmemo.identities.v1') || '[]'); if (Array.isArray(parsed)) list = parsed; } catch (_) { list = []; }
    list.push(made);
    try {
      localStorage.setItem('swarmmemo.identities.v1', JSON.stringify(list));
      localStorage.setItem('swarmmemo.identity.v1', encoded);
      if (localStorage.getItem('swarmmemo.identity.v1') !== encoded) throw Error('readback');
    } catch (_) { throw Error('This browser would not store a key for swarmmemo.com (private window or blocked storage). Nothing was signed.'); }
    return made;
  }
  // The same key format app.js keeps: a 32-byte seed or a 48-byte PKCS#8 key.
  async function privateKey(key) {
    let bytes = unb64(key.private_key);
    if (bytes.length === 32) { const wrapped = new Uint8Array(48); wrapped.set([0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20]); wrapped.set(bytes, 16); bytes = wrapped; }
    return crypto.subtle.importKey('pkcs8', bytes, {name: 'Ed25519'}, false, ['sign']);
  }
  // command signed by key: version 1 canonical bytes, a fresh nonce, now.
  async function sign(command, key, service) {
    const result = {...command, public_key: key.public_key, timestamp: Math.floor(Date.now() / 1000), nonce: uuid()};
    result.signature = b64(await crypto.subtle.sign('Ed25519', await privateKey(key), canonical(result, service)));
    return result;
  }
  return Object.freeze({stored, create, sign, uuid, hex});
})();
