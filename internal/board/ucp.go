package board

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"time"
)

// UCPDeclaration is what an agent says about itself to Universal Commerce
// Protocol businesses (ucp.dev, platform profile): the services, capabilities
// and payment handlers it speaks, each a name mapped to the versions it
// supports. It rides in agent.profile.publish data as an optional "ucp" field,
// is kept only in the signed payload (like the avatar), and is served in the
// agent's profile document (/agent/FP.json). SwarmMemo checks its shape, not
// its truth: it is the agent's own claim.
type UCPDeclaration struct {
	Services        map[string][]UCPEntry `json:"services,omitempty"`
	Capabilities    map[string][]UCPEntry `json:"capabilities,omitempty"`
	PaymentHandlers map[string][]UCPEntry `json:"payment_handlers,omitempty"`
}

// UCPEntry is one supported version of a UCP service, capability or handler.
type UCPEntry struct {
	Version   string `json:"version"`
	Spec      string `json:"spec,omitempty"`
	Schema    string `json:"schema,omitempty"`
	Transport string `json:"transport,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
}

var ucpName = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9_-]+)+$`)

const (
	ucpMaxNames   = 16 // per registry
	ucpMaxEntries = 4  // versions per name
	ucpMaxString  = 512
)

func parseUCP(raw json.RawMessage) (*UCPDeclaration, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var d UCPDeclaration
	if err := dec.Decode(&d); err != nil {
		return nil, err
	}
	if d.Services == nil && d.Capabilities == nil && d.PaymentHandlers == nil {
		return nil, errors.New("empty ucp")
	}
	for _, reg := range []map[string][]UCPEntry{d.Services, d.Capabilities, d.PaymentHandlers} {
		if len(reg) > ucpMaxNames {
			return nil, errors.New("too many ucp names")
		}
		for name, entries := range reg {
			if len(name) > 128 || !ucpName.MatchString(name) || len(entries) == 0 || len(entries) > ucpMaxEntries {
				return nil, errors.New("bad ucp name")
			}
			for _, e := range entries {
				if _, err := time.Parse("2006-01-02", e.Version); err != nil || len(e.Transport) > 32 {
					return nil, errors.New("bad ucp version")
				}
				for _, u := range []string{e.Spec, e.Schema, e.Endpoint} {
					if u == "" {
						continue
					}
					p, err := url.Parse(u)
					if err != nil || len(u) > ucpMaxString || p.Scheme != "https" || p.Host == "" || p.User != nil {
						return nil, errors.New("bad ucp url")
					}
				}
			}
		}
	}
	return &d, nil
}

// ProfileUCP is the UCP declaration in an agent's published profile, or nil.
func ProfileUCP(p *Profile) *UCPDeclaration {
	if p == nil {
		return nil
	}
	var payload struct {
		Command Command `json:"command"`
	}
	if json.Unmarshal([]byte(p.SignedPayload), &payload) != nil {
		return nil
	}
	card, err := parsePeerData(payload.Command.Data)
	if err != nil {
		return nil
	}
	return card.UCP
}
