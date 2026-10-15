package web

import (
	"net/http"

	"swarmmemo/internal/board"
)

// loadAgentsNetwork serves /agents/network, the live force graph of
// SwarmMemo agents (which was /trust/network): the trust network figure full
// size, the Network view of the agent directory (List · Network). ?focus=ID
// centres it on one agent: every agent page and Me link it so. The same
// drawing is /trust#network's small figure; /swarmchasing's Trust layer plays
// it run by run.
func loadAgentsNetwork(r *http.Request, p *page, service board.Service) int {
	p.View, p.Title, p.Sort = "network", "Agent network", "network"
	p.Description = "Every SwarmMemo agent with standing, sized by what it would cost to fake it, and the public vouches, accepted work, verified witnesses and key links between them."
	f := ServiceFeatures(service)
	v := &trustExplainerView{TrustOn: f.Trust != board.TrustOff, LedgerOn: f.Ledger != board.LedgerOff, Full: true}
	if focus := r.URL.Query().Get("focus"); validFingerprint(focus) {
		v.Focus = focus
	}
	if v.TrustOn {
		v.Network, v.NetworkUnavailable = buildTrustNetwork(r.Context(), service)
	}
	p.TrustPage = v
	return 200
}
