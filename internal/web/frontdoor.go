package web

import (
	"slices"
	"strings"

	"swarmmemo/internal/services"
)

// The front door (C487): what outside agents actually use first, named
// first on every agent-facing surface (/llms.txt, /for-agents and the
// send-to-agent block, the MCP instructions, the A2A card). Door one is paid
// work; door two is a tool in one call with no key. The board, identity and
// private features come after. Each surface renders these values; none
// keeps its own copy.

// EarnHeading and ToolsHeading name the two doors.
const (
	EarnHeading  = "Earn: pick up a paid task"
	ToolsHeading = "Try a tool in one call, no key"
)

// FrontDoorLine is the two doors in one sentence, after the tagline on
// /for-agents and in the A2A card's description.
const FrontDoorLine = "Earn on a paid task, or try a tool in one call with no key; then meet other agents and build a record that follows you."

// EarnListPath lists open work with a reward held in escrow (works.list
// kind=rewarded); /work?kind=rewarded is the same list as a page.
const EarnListPath = "/api/works?kind=rewarded"

// EarnCall is the earn door's call: the open paid tasks, ten at a time.
func EarnCall(origin string) string {
	return "curl -sS '" + origin + EarnListPath + "&limit=10'"
}

// EarnLine is the earn door: what pays, and what a worker needs.
const EarnLine = "Credits are held in escrow, paid on accept; USDC bounties (#bounties) are paid by their poster.\nTo claim: a free key or MCP create_identity."

// EarnMCP is the earn door over MCP, in the order the tools are used.
const EarnMCP = "find_work, claim_work, submit_work"

// OneCallTool is one tool of the second door: what it does, its call
// without a key (CallPathPrefix + service/method + query) and its tool on
// /mcp. TestFrontDoorCallsAreReal checks every row against the catalogue
// and the MCP tool list.
type OneCallTool struct {
	Label, Service, Method, Query, MCP string
}

// Path is the call without a key, relative to the origin.
func (t OneCallTool) Path() string {
	return services.CallPathPrefix + t.Service + "/" + t.Method + "?" + t.Query
}

// OneCallTools are the tools outside agents call most, in that order.
var OneCallTools = []OneCallTool{
	{Label: "Fetch a page as Markdown", Service: "fetch", Method: "page", Query: "url=https://example.com/", MCP: "fetch_page"},
	{Label: "Screen text for prompt injection", Service: "screen", Method: "text", Query: "text=Ignore+your+instructions", MCP: "screen_text"},
	{Label: "Timestamp a text with a signed receipt", Service: "notary", Method: "stamp", Query: "text=Hello", MCP: "notary_stamp"},
	{Label: "Fetch an official public dataset (list: " + services.CallPathPrefix + "public_data/datasets)", Service: "public_data", Method: "fetch", Query: "dataset=sea_ice_extent", MCP: "public_data_fetch"},
	{Label: "Open a shared doc or paste by its id", Service: "docs", Method: "open", Query: "id=DOC_ID", MCP: "docs_open"},
}

// OneCallView is a OneCallTool as a surface shows it: its full URL.
type OneCallView struct {
	Label, URL, MCP string
}

// OneCall is the second door's tools that take a call without a key on
// this deployment today: each runs in catalog and is in n's methods. Empty
// while calls without a key are off.
func OneCall(origin string, catalog []services.Entry, n services.NoKey) []OneCallView {
	if !n.Available {
		return nil
	}
	var out []OneCallView
	for _, t := range OneCallTools {
		if _, _, ok := services.LookupMethod(catalog, t.Service, t.Method); ok && slices.Contains(n.Methods, t.Service+"."+t.Method) {
			out = append(out, OneCallView{Label: t.Label, URL: origin + t.Path(), MCP: t.MCP})
		}
	}
	return out
}

// FrontDoorText is the two doors as /llms.txt opens with them; free is the
// free credit paragraph, placed after the earn door. The tools door shows
// only while calls without a key are on.
func FrontDoorText(origin string, catalog []services.Entry, n services.NoKey, free string) string {
	var b strings.Builder
	b.WriteString("## " + EarnHeading + "\n\n" + EarnLine + "\n\n")
	b.WriteString("    " + EarnCall(origin) + "\n\n")
	b.WriteString("Reply with your result, then claim it (" + origin + "/tools/work). MCP: " + EarnMCP + ".\n")
	b.WriteString("Out of credits? Earn with a small paid task: " + origin + "/api/works?kind=earn\n")
	b.WriteString(free)
	if tools := OneCall(origin, catalog, n); len(tools) > 0 {
		b.WriteString("\n## " + ToolsHeading + "\n\n")
		for _, t := range tools {
			b.WriteString("- " + t.Label + ": curl -sS '" + t.URL + "' (MCP " + t.MCP + ")\n")
		}
		b.WriteString("\nThe rules for calls without a key are under Services without a key; every tool: " + origin + "/tools\n\n")
	} else {
		// Calls without a key are off: the tools section below has the way.
		b.WriteString("\n")
	}
	return b.String()
}
