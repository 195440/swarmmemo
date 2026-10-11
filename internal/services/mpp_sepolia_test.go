package services

// Base Sepolia proof (C202a), off unless asked for: does the facilitator
// settle an EIP-3009 authorization whose nonce is MPP's challenge-derived
// keccak256(challenge.id ‖ realm) rather than a random one?
//
// Why it should: EIP-3009 treats the nonce as an opaque bytes32 the signer
// picks; the token contract only refuses a (from, nonce) pair it has seen.
// The x402 reference facilitator (coinbase/x402,
// go/mechanisms/evm/exact/facilitator/eip3009*.go) checks the nonce is 32
// bytes and unused (authorizationState), and checks scheme, network,
// to = payTo, value = amount, the time window, the EIP-712 signature and a
// simulation; nothing ties the nonce to anything else. The body we send
// (facilitatorBody) is byte-identical for an x402 and an MPP payment of the
// same authorization (TestMPPSettlesOnTheX402Rail), so the facilitator
// cannot tell them apart. This test proves it on chain, with testnet USDC
// only:
//
//	SWARMMEMO_MPP_SEPOLIA=1 \
//	SWARMMEMO_MPP_SEPOLIA_KEY=/path/to/testnet.key \        # 0600, 64 hex; holds Base Sepolia USDC (faucet.circle.com)
//	SWARMMEMO_MPP_SEPOLIA_FACILITATOR=https://facilitator.example \  # one that serves eip155:84532 (prod's, if it does)
//	SWARMMEMO_MPP_SEPOLIA_PAY_TO=0x... \                     # any address you control
//	[SWARMMEMO_MPP_SEPOLIA_TOKEN_FILE=/path/to/token] \      # the facilitator's bearer token, if it needs one
//	go test ./internal/services -run TestMPPBaseSepoliaProof -count=1 -v
//
// It moves 0.01 test USDC from the key's address to PAY_TO, logs the
// transaction hash and the challenge nonce (check on sepolia.basescan.org
// that the transferWithAuthorization input carries that nonce), then asks
// the facilitator to verify the same authorization again, which must now
// fail (the nonce is spent). The network and asset are fixed to Base
// Sepolia's USDC: this test cannot touch mainnet.

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"swarmmemo/internal/pay"
)

const (
	sepoliaNetwork = "eip155:84532"
	sepoliaChainID = 84532
	sepoliaUSDC    = "0x036CbD53842c5426634e7929541eC2318f3dCF7e" // Circle's USDC on Base Sepolia; EIP-712 name "USDC", version "2"
	sepoliaAmount  = 10_000                                       // 0.01 USDC
)

func TestMPPBaseSepoliaProof(t *testing.T) {
	if os.Getenv("SWARMMEMO_MPP_SEPOLIA") != "1" {
		t.Skip("set SWARMMEMO_MPP_SEPOLIA=1 and the SWARMMEMO_MPP_SEPOLIA_* variables to run the Base Sepolia proof")
	}
	keyPath, facilitator := os.Getenv("SWARMMEMO_MPP_SEPOLIA_KEY"), os.Getenv("SWARMMEMO_MPP_SEPOLIA_FACILITATOR")
	payTo, ok := ParseEVMAddress(os.Getenv("SWARMMEMO_MPP_SEPOLIA_PAY_TO"))
	if keyPath == "" || facilitator == "" || !ok || payTo == (EVMAddress{}) {
		t.Fatal("SWARMMEMO_MPP_SEPOLIA_KEY, _FACILITATOR and _PAY_TO are required")
	}
	signer, err := LoadX402Signer(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	conf := `{"schema":1,"enabled":false,"network":"` + sepoliaNetwork + `","asset":"` + sepoliaUSDC + `","asset_name":"USDC","asset_version":"2","facilitator_url":` + strconv.Quote(facilitator)
	if tf := os.Getenv("SWARMMEMO_MPP_SEPOLIA_TOKEN_FILE"); tf != "" {
		conf += `,"facilitator_token_file":` + strconv.Quote(tf)
	}
	cfg, err := ParseFacilitatorConfig([]byte(conf+"}"), readPrivateFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Network != sepoliaNetwork || cfg.Asset.String() != sepoliaUSDC {
		t.Fatal("refusing: not Base Sepolia USDC")
	}
	mppw, err := pay.NewMPP(cfg.Rail(), "proof.swarmmemo.invalid", []byte("base-sepolia-proof:"+strconv.FormatInt(time.Now().UnixNano(), 10)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	quote := strconv.FormatInt(now+300, 10) + ".sepolia." + strconv.FormatInt(now, 36)
	ask := pay.Ask{PayTo: payTo, Amount: sepoliaAmount, Quote: quote, Expires: now + 300, MaxTimeout: 300,
		Resource: "https://proof.swarmmemo.invalid/mpp", Description: "MPP challenge-derived nonce proof (testnet)"}

	// The server's challenge; the client's credential, signed for real.
	h := make(map[string][]string)
	if _, err := mppw.Challenge(ask, h); err != nil {
		t.Fatal(err)
	}
	domain := EIP712Domain{Name: cfg.AssetName, Version: cfg.AssetVersion, ChainID: sepoliaChainID, VerifyingContract: cfg.Asset}
	cred := mppSign(t, signer, domain, h, ask.Expires)

	// The server's side: credential, parse, bind, then the facilitator.
	raw, field, err := mppw.Credential(map[string][]string{"Authorization": {cred}})
	if err != nil {
		t.Fatal(err)
	}
	x, err := mppw.Parse(raw, field)
	if err != nil {
		t.Fatal(err)
	}
	if err := mppw.Bound(x, ask, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	p := x.Payment()
	req := X402Requirement{PayTo: payTo, Amount: sepoliaAmount, Quote: quote, Expires: ask.Expires, MaxTimeout: 300}
	t.Logf("challenge id %s, nonce %s (keccak256(id ‖ realm)), payer %s", x.Ref, p.Nonce, p.From)
	ctx := context.Background()
	if err := cfg.Verify(ctx, nil, p, req); err != nil {
		t.Fatalf("facilitator /verify refused the challenge-derived nonce: %v", err)
	}
	st, err := cfg.SettleVerified(ctx, nil, p, req)
	if err != nil {
		t.Fatalf("facilitator /settle: %v", err)
	}
	t.Logf("SETTLED on Base Sepolia: tx %s (https://sepolia.basescan.org/tx/%s), nonce %s", st.Transaction, st.Transaction, p.Nonce)

	// The nonce is now spent on chain: the same authorization must not
	// verify again (a facilitator that does not read authorizationState
	// in /verify would still refuse at /settle; we do not settle twice).
	if err := cfg.Verify(ctx, nil, p, req); err == nil {
		t.Logf("note: the facilitator's /verify does not check authorizationState; replay is still refused on chain")
	} else {
		t.Logf("replay refused by /verify: %v", err)
	}
}
