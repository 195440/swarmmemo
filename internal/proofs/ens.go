package proofs

// ENS and Basenames: a name proves itself through the key's verified wallet
// link. The name must resolve to one of the key's verified wallets (its
// controller pointed it there) and that wallet's primary name must be the
// name (the wallet chose it): both sides consent, read with eth_call over a
// public JSON-RPC endpoint (PROOF_ENS_RPC_URL for label.eth on Ethereum,
// PROOF_BASE_RPC_URL for label.base.eth on Base). Value input: the name's
// registration time, from its registrar's NameRegistered log (unknown counts
// only the base). Root: ens: and the name.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"swarmmemo/internal/pay"
	"swarmmemo/internal/trust"
)

// Defaults: public endpoints that need no account.
const (
	DefaultENSRPC  = "https://ethereum-rpc.publicnode.com"
	DefaultBaseRPC = "https://mainnet.base.org"
)

// ensChain is one naming system: its registry, registrar, parent name and
// reverse suffix.
type ensChain struct {
	rpc        string
	registry   string
	registrar  string
	fromBlock  int64
	parent     string // eth, base.eth
	reverseSfx string // addr.reverse, 80002105.reverse
}

// ENS is the ENS and Basenames provider.
type ENS struct {
	spec  Spec
	eth   ensChain
	base  ensChain
	f     fetcher
	topic string // NameRegistered(uint256,address,uint256)
}

func newENS(ethRPC, baseRPC string, f fetcher) *ENS {
	if ethRPC == "" {
		ethRPC = DefaultENSRPC
	}
	if baseRPC == "" {
		baseRPC = DefaultBaseRPC
	}
	t := pay.Keccak256([]byte("NameRegistered(uint256,address,uint256)"))
	return &ENS{spec: Spec{Kind: "ens", Label: "Link an ENS name or Basename", Proves: "A name you hold resolves to your verified wallet, and is that wallet's primary name; its age counts.",
		Reveals: "the name.", Flow: FlowWallet, Method: "ens-primary-name", Root: "`ens:` + the name", Needs: []string{NeedName, NeedWallet}, EffortMinutes: 10,
		Priced: "50 for holding a name + its registration age (up to 150, one-year ramp)", Value: "NAME.eth or NAME.base.eth",
		Action: `link a wallet first, set the name's address and the wallet's primary name to each other, then identity.link {"schema":1,"kind":"ens","value":NAME}`},
		eth:  ensChain{rpc: ethRPC, registry: "0x00000000000c2e074ec69a0dfb2997ba6c7d2e1e", registrar: "0x57f1887a8bf19b14fc0df6fd9b2acc9af147ea85", fromBlock: 9380410, parent: "eth", reverseSfx: "addr.reverse"},
		base: ensChain{rpc: baseRPC, registry: "0xb94704422c2a1e396835a571837aa5ae53285a95", registrar: "0x03c4738ee98ae44591e1a4a4f3cab6641d95dd9a", fromBlock: 17571480, parent: "base.eth", reverseSfx: "80002105.reverse"},
		f:    f, topic: "0x" + hex.EncodeToString(t[:])}
}

func (p *ENS) Spec() Spec    { return p.spec }
func (p *ENS) Enabled() bool { return p.eth.rpc != "" || p.base.rpc != "" }
func (p *ENS) Assess(pr Proof, now time.Time) int64 {
	return 50 + trust.AccountCents(ageDays(pr.Inputs["created_at"], now), 150, 365, 0, 0, 0)
}

// ensLabelRE is one second-level label: ASCII letters, digits and hyphens
// (names needing Unicode normalization are out of scope).
var ensLabelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])$`)

// NormalizeENS is a name's canonical form: lowercase NAME.eth or
// NAME.base.eth, a second-level name of at least three characters.
func NormalizeENS(raw string) (string, bool) {
	name := lowerASCII(strings.TrimSpace(raw))
	for _, parent := range []string{".base.eth", ".eth"} {
		if label, ok := strings.CutSuffix(name, parent); ok {
			return name, ensLabelRE.MatchString(label) && !strings.Contains(label, "--")
		}
	}
	return "", false
}

// Namehash is EIP-137's namehash of a name.
func Namehash(name string) [32]byte {
	var node [32]byte
	if name == "" {
		return node
	}
	labels := strings.Split(name, ".")
	for i := len(labels) - 1; i >= 0; i-- {
		l := pay.Keccak256([]byte(labels[i]))
		node = pay.Keccak256(node[:], l[:])
	}
	return node
}

func (p *ENS) chainOf(name string) ensChain {
	if strings.HasSuffix(name, ".base.eth") {
		return p.base
	}
	return p.eth
}

// Verify requires the name to resolve to one of the key's wallets and that
// wallet's primary name to be the name.
func (p *ENS) Verify(ctx context.Context, e Evidence) (Proof, error) {
	name, ok := NormalizeENS(e.Value)
	if !ok {
		return Proof{}, ErrFailed
	}
	pr := Proof{Kind: "ens", Value: name, Root: "ens:" + name, VerifiedAt: e.Now.Unix()}
	return p.check(ctx, pr, e.Wallets)
}

// Recheck repeats both resolutions.
func (p *ENS) Recheck(ctx context.Context, pr Proof, e Evidence) (Proof, error) {
	pr.VerifiedAt = e.Now.Unix()
	return p.check(ctx, pr, e.Wallets)
}

func (p *ENS) check(ctx context.Context, pr Proof, wallets []string) (Proof, error) {
	c := p.chainOf(pr.Value)
	if c.rpc == "" {
		return Proof{}, ErrDisabled
	}
	if len(wallets) == 0 {
		return Proof{}, ErrFailed
	}
	node := Namehash(pr.Value)
	addr, err := p.resolveAddr(ctx, c, node)
	if err != nil {
		return Proof{}, err
	}
	var wallet string
	for _, w := range wallets {
		if strings.EqualFold(w, addr) {
			wallet = lowerASCII(w)
		}
	}
	if wallet == "" {
		return Proof{}, ErrFailed
	}
	primary, err := p.reverseName(ctx, c, wallet)
	if err != nil {
		return Proof{}, err
	}
	if primary != pr.Value {
		return Proof{}, ErrFailed
	}
	pr.Inputs = Inputs{"created_at": p.registeredAt(ctx, c, pr.Value)}
	return pr, nil
}

// resolveAddr is addr(node) at the name's resolver ("" none: a failure).
func (p *ENS) resolveAddr(ctx context.Context, c ensChain, node [32]byte) (string, error) {
	resolver, err := p.resolverOf(ctx, c, node)
	if err != nil {
		return "", err
	}
	out, err := p.call(ctx, c, resolver, "3b3b57de", node)
	if err != nil {
		return "", err
	}
	if len(out) < 32 {
		return "", ErrFailed
	}
	return "0x" + hex.EncodeToString(out[12:32]), nil
}

// reverseName is the wallet's primary name on c.
func (p *ENS) reverseName(ctx context.Context, c ensChain, wallet string) (string, error) {
	node := Namehash(strings.TrimPrefix(wallet, "0x") + "." + c.reverseSfx)
	resolver, err := p.resolverOf(ctx, c, node)
	if err != nil {
		return "", err
	}
	out, err := p.call(ctx, c, resolver, "691f3431", node)
	if err != nil {
		return "", err
	}
	s, ok := abiString(out)
	if !ok {
		return "", ErrFailed
	}
	return lowerASCII(s), nil
}

func (p *ENS) resolverOf(ctx context.Context, c ensChain, node [32]byte) (string, error) {
	out, err := p.call(ctx, c, c.registry, "0178b8bf", node)
	if err != nil {
		return "", err
	}
	if len(out) < 32 {
		return "", ErrFailed
	}
	a := "0x" + hex.EncodeToString(out[12:32])
	if a == "0x0000000000000000000000000000000000000000" {
		return "", ErrFailed
	}
	return a, nil
}

// abiString decodes one ABI-encoded string return value.
func abiString(out []byte) (string, bool) {
	if len(out) < 64 {
		return "", false
	}
	off := new(big.Int).SetBytes(out[:32])
	if !off.IsInt64() || off.Int64() != 32 {
		return "", false
	}
	n := new(big.Int).SetBytes(out[32:64])
	if !n.IsInt64() || n.Int64() > 255 || int64(len(out)) < 64+n.Int64() {
		return "", false
	}
	return string(out[64 : 64+n.Int64()]), true
}

// registeredAt is the block time of the name's first NameRegistered log, or
// 0 when it cannot be read (a public endpoint may refuse wide log ranges).
func (p *ENS) registeredAt(ctx context.Context, c ensChain, name string) int64 {
	label := strings.TrimSuffix(name, "."+c.parent)
	id := pay.Keccak256([]byte(label))
	var logs []struct {
		BlockNumber string `json:"blockNumber"`
	}
	filter := map[string]any{"address": c.registrar, "topics": []any{p.topic, "0x" + hex.EncodeToString(id[:])},
		"fromBlock": "0x" + strconv.FormatInt(c.fromBlock, 16), "toBlock": "latest"}
	if p.rpc(ctx, c, "eth_getLogs", []any{filter}, &logs) != nil || len(logs) == 0 {
		return 0
	}
	var block struct {
		Timestamp string `json:"timestamp"`
	}
	if p.rpc(ctx, c, "eth_getBlockByNumber", []any{logs[0].BlockNumber, false}, &block) != nil {
		return 0
	}
	t, err := strconv.ParseInt(strings.TrimPrefix(block.Timestamp, "0x"), 16, 64)
	if err != nil || t <= 0 {
		return 0
	}
	return t
}

func (p *ENS) call(ctx context.Context, c ensChain, to, selector string, node [32]byte) ([]byte, error) {
	var out string
	if err := p.rpc(ctx, c, "eth_call", []any{map[string]string{"to": to, "data": "0x" + selector + hex.EncodeToString(node[:])}, "latest"}, &out); err != nil {
		return nil, err
	}
	b, err := hex.DecodeString(strings.TrimPrefix(out, "0x"))
	if err != nil {
		return nil, ErrUnavailable
	}
	return b, nil
}

// rpc is one JSON-RPC call; a revert reads as "no such record" (ErrFailed).
func (p *ENS) rpc(ctx context.Context, c ensChain, method string, params []any, into any) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := p.f.do(ctx, request{method: http.MethodPost, url: c.rpc, body: body, into: &resp}); err != nil {
		return err
	}
	if resp.Error != nil {
		if resp.Error.Code == 3 || resp.Error.Code == -32000 {
			return ErrFailed // execution reverted: no such record
		}
		return ErrUnavailable
	}
	if len(resp.Result) == 0 || json.Unmarshal(resp.Result, into) != nil {
		return ErrUnavailable
	}
	return nil
}
