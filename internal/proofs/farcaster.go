package proofs

// Farcaster: an FID proves itself through a public hub's HTTP API (no
// account, no key; FARCASTER_HUB_URL, an optional API key header for hosted
// hubs). Two ways, one statement (Statement("farcaster", …)):
//
//   - a cast: the FID casts the statement, and the hub's latest casts by the
//     FID contain it (proof "cast");
//   - a signature: the FID's custody address, or an Ethereum address the FID
//     verified, personal_signs the statement (proof "0x…"), checked against
//     the hub's ID registry and verification messages.
//
// Value inputs: the FID's registration time (its ID registry event) and its
// followers (follow links to it, counted up to farcasterFollowMax). Root:
// farcaster: and the FID.

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"swarmmemo/internal/services"
	"swarmmemo/internal/trust"
)

const (
	farcasterCastPage    = 100
	farcasterFollowMax   = 1000
	farcasterTextMax     = 1024
	DefaultFarcasterHub  = "https://hub.pinata.cloud"
	farcasterStatementOK = "cast"
)

// fidRE is a Farcaster id.
var fidRE = regexp.MustCompile(`^[1-9][0-9]{0,11}$`)

// Farcaster is the Farcaster provider.
type Farcaster struct {
	spec   Spec
	hub    string
	apiKey string
	f      fetcher
}

func newFarcaster(hub, apiKey string, f fetcher) *Farcaster {
	if hub == "" {
		hub = DefaultFarcasterHub
	}
	return &Farcaster{spec: Spec{Kind: "farcaster", Label: "Link Farcaster", Proves: "You control a Farcaster account (FID); its age and followers count.",
		Reveals: "the FID.", Flow: FlowStatement, Method: "farcaster-hub", Root: "`farcaster:` + the FID", Needs: []string{"farcaster"}, EffortMinutes: 5,
		Priced: "FID age (up to 120, one-year ramp) + followers (up to 80, saturating at 100)", Value: "the FID",
		ChallengeTTL: 3600,
		Action:       `standing.challenge {"schema":1,"kind":"farcaster","value":FID}, cast data.statement (or personal_sign it with the FID's custody or verified address), then identity.link {"schema":1,"kind":"farcaster","value":FID,"proof":"cast" or SIGNATURE,"nonce":NONCE}`},
		hub: strings.TrimRight(hub, "/"), apiKey: apiKey, f: f}
}

func (p *Farcaster) Spec() Spec    { return p.spec }
func (p *Farcaster) Enabled() bool { return p.hub != "" }
func (p *Farcaster) Assess(pr Proof, now time.Time) int64 {
	return trust.AccountCents(ageDays(pr.Inputs["created_at"], now), 120, 365, pr.Inputs["followers"], 80, 100)
}

// NormalizeFID is an FID's canonical form.
func NormalizeFID(raw string) (string, bool) { return raw, fidRE.MatchString(raw) }

func (p *Farcaster) get(ctx context.Context, path string, q url.Values, into any) error {
	h := http.Header{}
	if p.apiKey != "" {
		h.Set("x-api-key", p.apiKey)
	}
	return p.f.do(ctx, request{url: p.hub + path + "?" + q.Encode(), header: h, into: into})
}

// Verify checks the cast or the signature, then reads the FID's inputs.
func (p *Farcaster) Verify(ctx context.Context, e Evidence) (Proof, error) {
	fid, ok := NormalizeFID(e.Value)
	if !ok || e.Statement == "" {
		return Proof{}, ErrFailed
	}
	if e.Proof == farcasterStatementOK {
		if err := p.castHas(ctx, fid, e.Statement); err != nil {
			return Proof{}, err
		}
	} else {
		signer, ok := services.RecoverPersonalSign([]byte(e.Statement), e.Proof)
		if !ok {
			return Proof{}, ErrFailed
		}
		if err := p.addressControls(ctx, fid, signer.String()); err != nil {
			return Proof{}, err
		}
	}
	return p.inputs(ctx, Proof{Kind: "farcaster", Value: fid, Root: "farcaster:" + fid, VerifiedAt: e.Now.Unix()})
}

// Recheck reads the FID's inputs again; a vanished FID fails.
func (p *Farcaster) Recheck(ctx context.Context, pr Proof, e Evidence) (Proof, error) {
	pr.VerifiedAt = e.Now.Unix()
	return p.inputs(ctx, pr)
}

type hubMessages struct {
	Messages []struct {
		Data struct {
			Fid      int64 `json:"fid"`
			CastBody *struct {
				Text string `json:"text"`
			} `json:"castAddBody"`
			Verification *struct {
				Address string `json:"address"`
			} `json:"verificationAddAddressBody"`
		} `json:"data"`
	} `json:"messages"`
	NextPageToken string `json:"nextPageToken"`
}

// castHas reports whether one of the FID's latest casts contains statement.
func (p *Farcaster) castHas(ctx context.Context, fid, statement string) error {
	var m hubMessages
	if err := p.get(ctx, "/v1/castsByFid", url.Values{"fid": {fid}, "pageSize": {itoa(farcasterCastPage)}, "reverse": {"1"}}, &m); err != nil {
		return err
	}
	for _, msg := range m.Messages {
		if msg.Data.CastBody == nil || itoa(msg.Data.Fid) != fid {
			continue
		}
		text := msg.Data.CastBody.Text
		if len(text) > farcasterTextMax {
			text = text[:farcasterTextMax]
		}
		if strings.Contains(text, statement) {
			return nil
		}
	}
	return ErrFailed
}

// addressControls reports whether address is the FID's custody address or
// one it verified.
func (p *Farcaster) addressControls(ctx context.Context, fid, address string) error {
	var reg struct {
		Fid int64 `json:"fid"`
	}
	err := p.get(ctx, "/v1/onChainIdRegistryEventByAddress", url.Values{"address": {lowerASCII(address)}}, &reg)
	if err == nil && itoa(reg.Fid) == fid {
		return nil
	}
	if err != nil && err != ErrFailed {
		return err
	}
	var m hubMessages
	if err := p.get(ctx, "/v1/verificationsByFid", url.Values{"fid": {fid}}, &m); err != nil {
		return err
	}
	for _, msg := range m.Messages {
		if v := msg.Data.Verification; v != nil && strings.EqualFold(v.Address, address) {
			return nil
		}
	}
	return ErrFailed
}

// inputs reads the FID's registration time and followers.
func (p *Farcaster) inputs(ctx context.Context, pr Proof) (Proof, error) {
	var ev struct {
		Events []struct {
			BlockTimestamp int64 `json:"blockTimestamp"`
		} `json:"events"`
	}
	if err := p.get(ctx, "/v1/onChainEventsByFid", url.Values{"fid": {pr.Value}, "event_type": {"EVENT_TYPE_ID_REGISTER"}}, &ev); err != nil {
		return Proof{}, err
	}
	if len(ev.Events) == 0 {
		return Proof{}, ErrFailed
	}
	created := ev.Events[0].BlockTimestamp
	for _, x := range ev.Events {
		if x.BlockTimestamp > 0 && x.BlockTimestamp < created {
			created = x.BlockTimestamp
		}
	}
	var links hubMessages
	followers := int64(0)
	if p.get(ctx, "/v1/linksByTargetFid", url.Values{"target_fid": {pr.Value}, "link_type": {"follow"}, "pageSize": {itoa(farcasterFollowMax)}}, &links) == nil {
		followers = int64(min(len(links.Messages), farcasterFollowMax))
	}
	pr.Inputs = Inputs{"created_at": created, "followers": followers}
	return pr, nil
}
