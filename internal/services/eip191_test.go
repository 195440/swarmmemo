package services

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPersonalSignRoundTrip(t *testing.T) {
	key, _ := hex.DecodeString("0123456789012345678901234567890123456789012345678901234567890123")
	msg := []byte("example.org wants you to sign in with your Ethereum account:\n0x14791697260E4c9A71f18484C9f997B308e59325\n")
	addr, sig, err := SignPersonal(key, msg)
	if err != nil {
		t.Fatal(err)
	}
	// The ethers.js documentation's example key and its address.
	if addr.String() != "0x14791697260E4c9A71f18484C9f997B308e59325" {
		t.Fatalf("address %s", addr)
	}
	if got, ok := RecoverPersonalSign(msg, sig); !ok || got != addr {
		t.Fatalf("recover: %s %v", got, ok)
	}
	// v as 0/1 is accepted too.
	low := sig[:130] + map[string]string{"1b": "00", "1c": "01"}[sig[130:]]
	if got, ok := RecoverPersonalSign(msg, low); !ok || got != addr {
		t.Fatalf("recover with v 0/1: %s %v", got, ok)
	}
	// Another message recovers another address; malformed signatures fail.
	if got, ok := RecoverPersonalSign(append(msg, '!'), sig); ok && got == addr {
		t.Fatal("a changed message recovered the signer")
	}
	for _, bad := range []string{"", sig[2:], sig + "00", strings.ToUpper(sig[:2]) + sig[2:], sig[:130] + "1d", "0x" + strings.Repeat("zz", 65)} {
		if _, ok := RecoverPersonalSign(msg, bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}

// personalSignVectorFile is the shared vector the Python and JavaScript
// clients' link-wallet signers must reproduce byte for byte: RFC 6979, low s,
// v 27 or 28, as SignPersonal (and a wallet) signs. With
// SWARMMEMO_WRITE_GENERATED=1 this writes it (go generate).
const personalSignVectorFile = "../../testdata/wallet_sign.json"

var personalSignCases = []struct{ name, key, message string }{
	{"siwe", "0123456789012345678901234567890123456789012345678901234567890123",
		"swarmmemo.com wants you to sign in with your Ethereum account:\n0x14791697260E4c9A71f18484C9f997B308e59325\n\n" +
			"Link this address to SwarmMemo agent 4f5d0c1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6 to raise its standing.\n\n" +
			"URI: https://swarmmemo.com\nVersion: 1\nChain ID: 1\nNonce: 0123456789abcdef0123456789abcdef\n" +
			"Issued At: 2026-10-15T00:00:00Z\nExpiration Time: 2026-10-15T00:15:00Z"},
	{"key-one-empty", "0000000000000000000000000000000000000000000000000000000000000001", ""},
	{"key-n-minus-one-utf8", "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364140", "héllo ✓"},
	// The 29-byte prefix (its length "107" included) and 107 bytes fill one Keccak block exactly.
	{"block-edge", "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318", strings.Repeat("a", 107)},
	// 29 + 106 = 135 bytes: the 0x01 and 0x80 padding bits share the block's last byte (0x81).
	{"pad-one-byte", "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318", strings.Repeat("a", 106)},
	// Three Keccak blocks (29 + 300 bytes).
	{"three-blocks", "000000000000000000000000000000000000000000000000000000000000000b", strings.Repeat("b", 300)},
	// web3.js's documented eth.accounts.sign("Some data", key) result, the same bytes.
	{"web3js-some-data", "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318", "Some data"},
}

func personalSignVectors(t *testing.T) []byte {
	type vector struct {
		Name      string `json:"name"`
		Key       string `json:"key"`
		Message   string `json:"message"`
		Address   string `json:"address"`
		Signature string `json:"signature"`
	}
	doc := struct {
		About   string   `json:"about"`
		Vectors []vector `json:"vectors"`
	}{About: "C343: personal_sign (EIP-191) by internal/services SignPersonal, written by go generate ./internal/services: RFC 6979, low s, r || s || v with v 27 or 28. The clients' link-wallet signers must give the same address (EIP-55) and signature for each key and message. Test keys only."}
	for _, tc := range personalSignCases {
		key, _ := hex.DecodeString(tc.key)
		addr, sig, err := SignPersonal(key, []byte(tc.message))
		if err != nil {
			t.Fatal(tc.name, err)
		}
		if got, ok := RecoverPersonalSign([]byte(tc.message), sig); !ok || got != addr {
			t.Fatalf("%s: does not recover", tc.name)
		}
		doc.Vectors = append(doc.Vectors, vector{tc.name, tc.key, tc.message, addr.String(), sig})
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestPersonalSignVectorFile(t *testing.T) {
	want := personalSignVectors(t)
	if os.Getenv("SWARMMEMO_WRITE_GENERATED") == "1" {
		if err := os.WriteFile(personalSignVectorFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(personalSignVectorFile)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s is stale; run go generate ./internal/services", personalSignVectorFile)
	}
}
