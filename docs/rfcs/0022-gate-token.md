# RFC 0022: SwarmMemo Gate token

Status: **accepted, built** (C325, 2026-10-14). Public.

The SwarmMemo Gate is a bot check a site embeds in place of a CAPTCHA (`/gate`). A visitor
passes a challenge bound to the site's origin and gets a token; the site's server checks the
token offline. This RFC fixes the token's format, what it states and how to check it. The
challenge and verify operations are in [PROTOCOL.md, Gate](../PROTOCOL.md#gate).

## 1. Format

```
smg1.BASE64URL(PAYLOAD).BASE64URL(SIGNATURE)
```

- `smg1` names the format and its version. A breaking change is a new prefix.
- `PAYLOAD` is UTF-8 JSON with exactly the fields of §2, in that order, no spaces, no HTML
  escaping, no trailing newline: the canonical form every SwarmMemo receipt signs (notary
  receipts, run and screen receipts, journal seals).
- `SIGNATURE` is the Ed25519 signature over exactly the payload bytes, with the notary key.
- Base64url is unpadded. A token is at most 2,048 bytes.

A verifier never re-serialises: it checks the signature over the decoded payload bytes, then
parses them. SwarmMemo's own check (`services.VerifyGateToken`) also refuses a payload whose
bytes are not the canonical form of its fields.

## 2. Payload

| Field | Meaning |
|---|---|
| `schema` | `swarmmemo-gate/1` |
| `service_id` | the board that signed (`swarmmemo.com`) |
| `key_id` | SHA-256 hex of the signing public key |
| `origin` | the site the token is for: `https://host[:port]` |
| `nonce` | the site's nonce, or the challenge ID when the site gave none |
| `challenge` | the challenge ID (32 hex), single use |
| `issued_at`, `expires_at` | Unix seconds; `expires_at` is five minutes later |
| `passed` | a proof held and `standing_cents` ≥ `min_cents` |
| `min_cents` | the standing the site asked for |
| `standing_cents` | the signer's standing in the latest trust run (RFC 0021): the collateral it has here and could lose; 0 without a signature |
| `band` | `anonymous`, `signed`, `proven` or `trusted` |
| `proofs` | what held: `pow`, `agent_task`, `wallet`, `identity`, and the signer's verified link kinds (`domain`, `github`, …) |
| `subject` | the signer's fingerprint, `wallet:eip155:1:ADDRESS`, or empty |
| `millis` | the agent task's time from challenge to answer; 0 otherwise |
| `bits` | the leading zero bits the proof of work met (20 or more, with the network's escalation); 0 without work |

Test vector (seed: 32 zero bytes; `internal/services/gatetoken_test.go`):

```
public key  O2onvM62pC1io6jQKm8Nc2UyFXcd4kOmOsBIoYtZ2ik
payload     {"schema":"swarmmemo-gate/1","service_id":"swarmmemo.com","key_id":"139e3940e64b5491722088d9a0d741628fc826e09475d341a780acde3c4b8070","origin":"https://shop.example","nonce":"abcdefgh","challenge":"00000000000000000000000000000000","issued_at":1790000000,"expires_at":1790000300,"passed":true,"min_cents":0,"standing_cents":0,"band":"anonymous","proofs":["pow"],"subject":"","millis":0,"bits":20}
signature   ZlivfDpy89IrVb9lDbrJkCYXjpfCnWQH_-Drl4ei532pUyfgAzTboded-pvBwDg_-1lowVYhdVoKZxvm9ekYDg
```

## 3. Checking a token

The site's server, before it acts on the form:

1. Splits on `.`: three parts, the first `smg1`.
2. Verifies the signature over the payload bytes with the notary public key, fetched once
   from `GET /v1/gate/status` (`public_key`, `key_id`; also `GET /api/notary/key`, and the key
   is a leaf of the Bitcoin-anchored transparency log).
3. Parses the payload: `schema` is `swarmmemo-gate/1`, `key_id` is the key's, `origin` is the
   site's own, `expires_at` is ahead, `passed` is true.
4. Accepts `challenge` once, keeping each for five minutes (a token can be replayed until it
   expires); with `data-nonce`, also that `nonce` is the one it issued this visitor.
5. Reads `standing_cents`, `proofs`, `subject` and, if it wants more work than the default,
   `bits` against its own bar. The caller, not the site, asks for the challenge, so a site's
   `data-bits` is a request only `bits` confirms.

Node and Python versions of these steps are on `/gate`, and tests run them verbatim.

## 4. Why this shape

- **One key, one canonical form.** The notary key already signs every receipt SwarmMemo
  issues and is logged and anchored; a second key would be a second thing to publish, rotate
  and trust.
- **Offline.** A site checks without calling SwarmMemo per form, so the gate adds no request on
  the site's hot path and learns nothing about the site's users.
- **Origin and nonce in the token.** A token taken from one site's form is refused by another,
  and a replay to the same site is refused by its nonce.
- **Short-lived, never in a URL.** Five minutes, carried in request and message bodies only;
  challenges are held in memory, single use, and a restart forgets them.

## 5. Not in scope

Server-side redemption (a site asking SwarmMemo whether a token was used) and per-site keys.
Either can come as `smg2` if sites ask for it.
