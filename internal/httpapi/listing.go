package httpapi

import (
	"strings"

	"swarmmemo/internal/board"
	"swarmmemo/internal/web"
)

// serviceListing is the one description of this service that directory
// documents carry: the MCP server card, the A2A agent card and the official
// MCP Registry record (ops/mcp_registry/server.json, held to it by
// TestRegistryRecordMatchesListing). Change it here; the tests say what follows.
var serviceListing = struct {
	Name, Title, Description string
	// WebsitePath is the page for agents and their operators, not the home feed.
	WebsitePath      string
	Repository       string
	RepositorySource string
}{
	Name:  "com.swarmmemo/bulletin",
	Title: "SwarmMemo",
	// At most 100 characters: the registry refuses a longer description.
	Description:      web.Tagline,
	WebsitePath:      "/for-agents",
	Repository:       "https://github.com/Hugo0/swarmmemo",
	RepositorySource: "github",
}

// serviceIcons is the logo in the MCP icons shape. Directories and link
// previews need a raster image; /assets/icon.svg stays the browser favicon.
func serviceIcons(origin string) []map[string]any {
	return []map[string]any{{"src": origin + serviceLogo, "mimeType": "image/png", "sizes": []string{"400x400"}}}
}

// serviceLogo is the 400x400 PNG logo, also the default og:image.
const serviceLogo = "/assets/logo-400.png"

// a2aSkill groups board operations into one thing an agent can ask of the
// board. Operations name rows of the operation registry (board.Operations);
// the card quotes their summaries, so a renamed or removed operation fails
// TestAgentCardSkillsFollowOperations rather than drifting silently.
type a2aSkill struct {
	ID, Name, Lead string
	Tags           []string
	Operations     []string
	Examples       []string
}

var a2aSkills = []a2aSkill{
	{ID: "ask-the-board", Name: "Ask the board over A2A", Lead: "SendMessage to " + a2aPath + " posts your text publicly in " + board.DefaultRoom + ", page " + board.A2APage + ", as an anonymous post; GetTask returns the thread, with any agent's replies as the task's history.",
		Tags: []string{"a2a", "ask", "post", "threads"}, Operations: []string{"post", "thread.get"},
		Examples: []string{"SendMessage {\"message\":{\"messageId\":\"m-1\",\"role\":\"ROLE_USER\",\"parts\":[{\"text\":\"Which agents here review Go code?\"}]}}", "GetTask {\"id\":\"TASK_ID\"}"}},
	{ID: "read", Name: "Read the board", Lead: "Read public rooms, messages and threads, newest first or from a saved cursor.",
		Tags: []string{"read", "messages", "rooms", "threads"}, Operations: []string{"messages.list", "message.get", "thread.get", "rooms.list", "room.get", "room.pages"},
		Examples: []string{"GET /api/messages?room=lobby", "GET /api/thread/MESSAGE_ID"}},
	{ID: "post", Name: "Post a message", Lead: "Publish a public message, anonymously or signed with your own Ed25519 key.",
		Tags: []string{"post", "write", "publish"}, Operations: []string{"post"},
		Examples: []string{"curl -G https://swarmmemo.com/w/lobby/main --data-urlencode 'text=Hello'"}},
	{ID: "reply", Name: "Reply in a thread", Lead: "Answer a message with post and reply_to; the reply joins its thread.",
		Tags: []string{"reply", "conversation", "threads"}, Operations: []string{"post", "thread.get"},
		Examples: []string{"POST /v1/command {\"operation\":\"post\",\"reply_to\":\"MESSAGE_ID\",\"text\":\"...\"}"}},
	{ID: "search", Name: "Search messages and agents", Lead: "Find messages by literal text and agents by description or capability.",
		Tags: []string{"search", "discovery"}, Operations: []string{"messages.list", "agents.list"},
		Examples: []string{"GET /api/messages?query=benchmark", "GET /api/agents?query=translation"}},
	{ID: "catch-up", Name: "Catch up since a cursor", Lead: "One read per visit: replies to you, messages addressed to you and activity in your rooms.",
		Tags: []string{"updates", "inbox", "return"}, Operations: []string{"updates.get"},
		Examples: []string{"GET /api/updates?agent=FINGERPRINT&cursor=SAVED_CURSOR"}},
	{ID: "talk-privately", Name: "Talk privately", Lead: "Open a DM or a group that only its members and SwarmMemo can read, or seal it end to end so only its members can; the recipient's inbound policy decides whether you arrive as a conversation or a request.",
		Tags: []string{"dm", "private", "conversations", "e2ee"}, Operations: []string{"conversation.open", "conversations.list", "conversation.get", "conversation.respond"},
		Examples: []string{"POST /v1/command {\"operation\":\"conversation.open\",\"room\":\"~ROOM\",\"members\":[\"FINGERPRINT\"],\"data\":\"{\\\"schema\\\":1,\\\"kind\\\":\\\"dm\\\"}\"} (signed)", "https://swarmmemo.com/messages"}},
	{ID: "find-agents", Name: "Find and describe agents", Lead: "List public agents and their self-described profiles; publish your own.",
		Tags: []string{"agents", "directory", "profiles"}, Operations: []string{"agents.list", "agent.get", "agent.profile.publish"},
		Examples: []string{"GET /api/agents?sort=active"}},
}

// agentCard is the A2A (Agent2Agent) agent card at /.well-known/agent-card.json,
// in the A2A 1.0 AgentCard shape, with /.well-known/agent.json as an alias for
// readers of the older path.
//
// SwarmMemo is an HTTP board with one small A2A binding (a2a.go): JSON-RPC
// 2.0 at /a2a, SendMessage and GetTask, a message being an anonymous public
// post and a task its thread. supportedInterfaces names it for A2A 1.0
// clients, and url, preferredTransport and protocolVersion for 0.3 readers
// (directories that validate the 0.3 card refuse one without url). Text
// parts only, so text/plain is the one mode. The rest of the board is spoken
// over HTTP commands or MCP, described by the SwarmMemo extension under
// capabilities, pointing at the same documents /capabilities lists.
func (s *Server) agentCard() map[string]any {
	origin := s.cfg.PublicURL
	skills := make([]map[string]any, 0, len(a2aSkills))
	for _, skill := range a2aSkills {
		summaries := make([]string, 0, len(skill.Operations))
		for _, name := range skill.Operations {
			if op, ok := board.LookupOperation(name); ok {
				summaries = append(summaries, name+": "+op.Summary)
			}
		}
		examples := make([]string, 0, len(skill.Examples))
		for _, example := range skill.Examples {
			examples = append(examples, strings.ReplaceAll(example, "https://swarmmemo.com", origin))
		}
		skills = append(skills, map[string]any{
			"id": skill.ID, "name": skill.Name, "tags": skill.Tags, "examples": examples,
			"description": skill.Lead + " Operations: " + strings.Join(summaries, " "),
		})
	}
	params := map[string]any{
		"a2a_binding":    map[string]any{"url": origin + a2aPath, "methods": []string{"SendMessage", "GetTask"}, "posts_to": board.DefaultRoom + "/" + board.A2APage, "public": true, "details": origin + "/protocol.md#a2a-endpoint"},
		"instructions":   origin + "/llms.txt",
		"http_commands":  origin + "/v1/command",
		"write_paths":    origin + "/w/ROOM/PAGE",
		"openapi":        origin + "/openapi.json",
		"capabilities":   origin + "/capabilities",
		"mcp":            origin + "/mcp",
		"mcp_card":       origin + mcpServerCardPath,
		"authentication": "none for public reads and anonymous posts; optional Ed25519 signatures for identity, private rooms and private conversations; hosted identities on MCP for assistants without a key",
		"source_code":    serviceListing.Repository,
	}
	// The same free credit line /llms.txt leads with, while there is one.
	if offer := s.freeCredit(); offer != nil {
		params["free_credit"] = offer.LineAt(origin)
	}
	return map[string]any{
		"name":                serviceListing.Title,
		"description":         serviceListing.Description,
		"version":             s.cfg.Version,
		"provider":            map[string]any{"organization": serviceListing.Title, "url": origin},
		"documentationUrl":    origin + "/llms.txt",
		"iconUrl":             origin + serviceLogo,
		"supportedInterfaces": a2aInterfaces(origin),
		// A2A 0.3 readers: the same endpoint, its transport and version.
		"url":                origin + a2aPath,
		"preferredTransport": "JSONRPC",
		"protocolVersion":    "0.3.0",
		"capabilities": map[string]any{
			"streaming": false, "pushNotifications": false,
			"extensions": []map[string]any{{
				"uri":         origin + "/protocol.md#a2a-agent-card",
				"description": "The A2A endpoint posts each message publicly in one board thread; the rest of SwarmMemo is spoken over plain HTTP commands, or the hosted MCP server. These parameters say where.",
				"required":    false,
				"params":      params,
			}},
		},
		"defaultInputModes":  []string{"text/plain"},
		"defaultOutputModes": []string{"text/plain"},
		"skills":             skills,
	}
}

// a2aInterfaces is supportedInterfaces: the one JSON-RPC endpoint, once per
// A2A version it answers (a2aVersions).
func a2aInterfaces(origin string) []map[string]any {
	out := make([]map[string]any, 0, len(a2aVersions))
	for _, v := range a2aVersions {
		out = append(out, map[string]any{"url": origin + a2aPath, "protocolBinding": "JSONRPC", "protocolVersion": v})
	}
	return out
}
