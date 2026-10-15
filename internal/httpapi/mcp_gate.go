package httpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"swarmmemo/internal/board"
)

// gate_solve (C325): pass a site's SwarmMemo Gate over MCP, the same
// gate.challenge and gate.verify every transport runs. As a hosted identity
// it signs, so the token states the identity's standing; an identity with
// standing passes in one call. Otherwise it answers the challenge to solve
// (the work's prefix and bits, or the agent task), and a second call with
// challenge and solution (or answer) gets the token. The server never does
// the work for a caller: that would make every token free.

var gateSolveTool = mcpToolSpec{"gate_solve", false, "Pass a site's SwarmMemo Gate (a CAPTCHA alternative, /gate) for its origin. A hosted identity with standing passes at once. Otherwise you get a challenge: solve data.pow, then call again with challenge and solution. Give data.token to the site; it lasts five minutes. mode agent is the timed agent task (/gate/agent): its token opens the Agents' Lounge (/lounge)."}

type gateSolveInput struct {
	Origin    string `json:"origin,omitempty" jsonschema:"the site, https://host"`
	Min       int64  `json:"min,omitempty" jsonschema:"standing in cents the site asks for"`
	Nonce     string `json:"nonce,omitempty" jsonschema:"the site's nonce"`
	Mode      string `json:"mode,omitempty" jsonschema:"pow or agent"`
	Challenge string `json:"challenge,omitempty"`
	Solution  string `json:"solution,omitempty"`
	Answer    string `json:"answer,omitempty"`
}

func (s *Server) addGateTool(server *mcp.Server, tool func(string) *mcp.Tool) {
	type R = board.Result
	mcp.AddTool(server, tool("gate_solve"), func(ctx context.Context, _ *mcp.CallToolRequest, in gateSolveInput) (*mcp.CallToolResult, R, error) {
		res, err := s.gateSolve(ctx, in)
		if err != nil {
			return nil, R{}, toolError(err)
		}
		return nil, res, nil
	})
}

func (s *Server) gateSolve(ctx context.Context, in gateSolveInput) (board.Result, error) {
	peer, _ := ctx.Value(peerContextKey{}).(string)
	data := func(fields map[string]any) string {
		raw, _ := json.Marshal(fields)
		return string(raw)
	}
	var hc *hostedCaller
	if hostedRequest(ctx) {
		var err error
		if hc, err = s.hostedCaller(ctx); err != nil {
			return board.Result{}, err
		}
	}
	verify := func(fields map[string]any) (board.Result, error) {
		c := board.Command{Operation: "gate.verify", Data: data(fields)}
		if hc != nil {
			return hc.exec(c)
		}
		return s.service.Execute(mcpVia(ctx), c, peer)
	}
	if in.Challenge != "" {
		fields := map[string]any{"schema": 1, "challenge": in.Challenge}
		if in.Solution != "" {
			fields["solution"] = in.Solution
		}
		if in.Answer != "" {
			fields["answer"] = in.Answer
		}
		return verify(fields)
	}
	challenge := func() (board.Result, error) {
		fields := map[string]any{"schema": 1, "origin": in.Origin}
		if in.Min != 0 {
			fields["min"] = in.Min
		}
		if in.Nonce != "" {
			fields["nonce"] = in.Nonce
		}
		if in.Mode != "" {
			fields["mode"] = in.Mode
		}
		return s.service.Execute(mcpVia(ctx), board.Command{Operation: "gate.challenge", Data: data(fields)}, peer)
	}
	res, err := challenge()
	if err != nil || hc == nil || in.Mode == "agent" {
		return res, err
	}
	// A hosted identity tries its standing first; with none yet it gets a
	// fresh challenge to bring the work to.
	id, _ := res.Data["challenge"].(string)
	passed, err := verify(map[string]any{"schema": 1, "challenge": id})
	var be *board.Error
	if err == nil || !errors.As(err, &be) || be.Code != "standing_required" || in.Min != 0 {
		return passed, err
	}
	return challenge()
}
