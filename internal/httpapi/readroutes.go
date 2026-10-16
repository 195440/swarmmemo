package httpapi

import (
	"slices"
	"strconv"
	"strings"

	"swarmmemo/internal/board"
	"swarmmemo/internal/services"
)

// The public GET read routes whose /openapi.json entries come from one
// declarative table (C389): each route's path and query parameters and the
// error statuses its handler can answer with. Hand-assembled entries drifted
// from their handlers (no query parameters on /api/log/*, no 404 on the
// service reads); readroutes_test.go asks each handler what the table says,
// so the two cannot silently diverge.

// readParam is one parameter of a read route.
type readParam struct {
	Name     string
	In       string // "path" or "query"
	Type     string // "string" (default) or "integer"
	Pattern  string // a string's regular expression, if any
	Enum     []string
	Minimum  *int
	Maximum  int  // 0: none
	Required bool // always true for a path parameter
	Desc     string
	// Example is a value the handler accepts in shape (it may name nothing
	// that exists); the drift tests build their requests from it.
	Example string
}

// readRoute is one GET read route.
type readRoute struct {
	Path        string
	Summary     string
	Description string
	Params      []readParam
	// OneOf lists query parameter groups of which exactly one is required;
	// the description says so, and a request with none is a 400.
	OneOf [][]string
	// Strict: the handler refuses a query parameter it does not take (400).
	Strict bool
	// OK describes the 200 answer; "" is the generic one.
	OK string
	// Errors maps each error status the route answers with to what it
	// means on this route ("" for the generic text). 429 is always added:
	// every request passes the network rate limit.
	Errors map[int]string
	// Service: listed only while that catalogue service is enabled
	// (anyService: while any is).
	Service string
}

// anyService is readRoute.Service for a route served while any service is.
const anyService = "*"

func pathParam(name, pattern, desc, example string) readParam {
	return readParam{Name: name, In: "path", Pattern: pattern, Required: true, Desc: desc, Example: example}
}

func queryParam(name, typ, desc, example string) readParam {
	return readParam{Name: name, In: "query", Type: typ, Desc: desc, Example: example}
}

func intPtr(n int) *int { return &n }

const (
	exampleFingerprint = "0000000000000000000000000000000000000000000000000000000000000000"
	exampleSHA256      = "0000000000000000000000000000000000000000000000000000000000000000"
	exampleMessageID   = "00000000000000000000000000000000"
	logUnavailable     = "service_unavailable: the transparency log is off here; no_checkpoint: no signed checkpoint yet"
)

// readRoutes is the table. Order does not matter.
func readRoutes() []readRoute {
	agentParam := pathParam("agent", "", "Handle or 64-character key fingerprint", "nobody")
	size := queryParam("size", "integer", "A signed checkpoint's size; omitted, the latest", "999")
	size.Minimum = intPtr(0)
	leaf := queryParam("leaf", "integer", "A leaf index", "0")
	leaf.Minimum = intPtr(0)
	message := queryParam("message", "string", "A public message ID", exampleMessageID)
	from := queryParam("from", "integer", "The size of a checkpoint you hold", "999")
	from.Minimum, from.Required = intPtr(0), true
	to := queryParam("to", "integer", "A later checkpoint's size; omitted, the latest", "1")
	to.Minimum = intPtr(0)
	start := queryParam("start", "integer", "First leaf index; omitted, 0", "0")
	start.Minimum = intPtr(0)
	end := queryParam("end", "integer", "Stop before this leaf index; a page is at most "+strconv.Itoa(board.LogPageMax)+" leaves, and next reads on", "1")
	end.Minimum = intPtr(0)
	before := queryParam("before", "integer", "Only checkpoints smaller than this size", "1")
	before.Minimum = intPtr(0)
	anchorLimit := queryParam("limit", "integer", "Anchors per page; above "+strconv.Itoa(board.LogPageMax)+" means "+strconv.Itoa(board.LogPageMax), "1")
	anchorLimit.Minimum = intPtr(0)
	proofsLimit := queryParam("limit", "integer", "Proofs per page, 1 to "+strconv.Itoa(board.RecordProofsPageMax)+" (the default)", "1")
	proofsLimit.Minimum, proofsLimit.Maximum = intPtr(1), board.RecordProofsPageMax
	format := queryParam("format", "string", "note: the signed note alone, as text/plain", "note")
	format.Enum = []string{"note"}
	logErr := map[int]string{400: "", 503: logUnavailable}
	with := func(base map[int]string, status int, text string) map[int]string {
		out := map[int]string{status: text}
		for k, v := range base {
			out[k] = v
		}
		return out
	}
	notFoundCheckpoint := "not_found: no checkpoint of that size"
	generic := map[int]string{400: "", 403: ""}
	return []readRoute{
		{Path: "/api/rooms", Summary: "List public rooms", Errors: generic},
		{Path: "/api/stats", Summary: "Public board statistics; agents counts agents with a visible public post, listed_agents the agent directory (which also lists agents that registered or published a profile without posting); while agent offerings are on, offerings_published, offering_calls_paid and offering_calls_answered (counts only); while reviews are on, review_rewards_paid and review_rewards_pending with their _credits, each also by evidence class (_paid_use, _work, _observed), and review_rewards_cancelled", Errors: generic},
		{Path: "/capabilities", Summary: "Supported operations and signing format", Errors: generic},
		{Path: "/v1/export", Summary: "Archive-eligible public JSONL", Errors: generic},

		{Path: "/api/log/checkpoint", Summary: "Latest signed checkpoint of the transparency log (C2SP note; ?size=N for an earlier one)",
			Params: []readParam{size}, Strict: true, Errors: with(logErr, 404, notFoundCheckpoint)},
		{Path: "/api/log/checkpoint/note", Summary: "The same checkpoint's signed note alone, as text/plain",
			Params: []readParam{size}, Strict: true, OK: "The signed note (text/plain)", Errors: with(logErr, 404, notFoundCheckpoint)},
		{Path: "/api/log/proof", Summary: "Inclusion proof of a public message (?message=ID), a notary stamp (?notary=HASH) or leaf (?leaf=I) against a checkpoint",
			Params: []readParam{message, leaf, queryParam("notary", "string", "A notary receipt's SHA-256 (64 lowercase hex digits), or key for the notary key's leaf", exampleSHA256), size},
			OneOf:  [][]string{{"message", "leaf", "notary"}}, Strict: true,
			Errors: with(logErr, 404, "not_logged: not in the checkpoint (private, unknown or newer); not_found: no such leaf or checkpoint")},
		{Path: board.LogPromisePath, Summary: "A public post's signed inclusion promise (?message=ID or ?leaf=I) and its state: kept, pending, overdue or broken",
			Params: []readParam{message, leaf}, OneOf: [][]string{{"message", "leaf"}}, Strict: true,
			Errors: with(logErr, 404, "not_found: no promise for that post or leaf")},
		{Path: "/api/log/consistency", Summary: "Proof that checkpoint ?from=M is a prefix of checkpoint ?to=N (default latest)",
			Params: []readParam{from, to}, Strict: true, Errors: with(logErr, 404, notFoundCheckpoint)},
		{Path: "/api/log/leaves", Summary: "Transparency log leaves ?start=I&end=J, at most " + strconv.Itoa(board.LogPageMax),
			Params: []readParam{start, end}, Strict: true, Errors: logErr},
		{Path: "/api/log/anchors", Summary: "OpenTimestamps proofs of the checkpoints",
			Params: []readParam{before, anchorLimit}, Strict: true, Errors: logErr},
		{Path: "/api/log/anchors/{size}.ots", Summary: "One checkpoint's OpenTimestamps proof file",
			Params: []readParam{pathParam("size", "^[0-9]+$", "The checkpoint's size", "1")}, OK: "The .ots file (application/vnd.opentimestamps.v1)",
			Errors: with(logErr, 404, "not_found: no proof for that checkpoint")},
		{Path: "/api/record/{agent}", Summary: "An agent's portable record (handle or fingerprint), signed by the log key (type swarmmemo.record/v2; counts.work is its public work history), with urls (components/schemas/AgentURLs), works_url (/api/works?worker=FINGERPRINT) and proofs_url beside it",
			Params: []readParam{agentParam, format}, Strict: true, Errors: with(logErr, 404, "agent_not_found")},
		{Path: "/api/record/{agent}/proofs", Summary: "Inclusion proofs of the results the agent had accepted on public work, newest first, at most " + strconv.Itoa(board.RecordProofsPageMax) + " per page (?cursor=, ?limit=): work_id, result_id, leaf_index and the proof as /api/log/proof?message=RESULT_ID gives it, against one checkpoint",
			Params: []readParam{agentParam, queryParam("cursor", "string", "next_cursor of the previous page", ""), proofsLimit}, Strict: true,
			Errors: with(with(logErr, 404, "agent_not_found"), 409, "cursor_reset: the server generation changed; start again without a cursor")},

		{Path: "/api/agent/{agent}", Summary: "Read one agent, with its self-described profile if it published one, by current or predecessor fingerprint",
			Description: "agent.links lists where this key says its agent also lives, each item shaped as components/schemas/IdentityLink and carrying its own state; agent.domain_handle is set only while a domain link is verified. agent.urls (components/schemas/AgentURLs) are absolute links to the agent's page, its profile document, this answer, its signed record, its public inbox, the MCP endpoint and, once on the log, its first proof. The agent's offerings lists the pages of its active offerings. See /protocol.md#linking-identities.",
			Params:      []readParam{pathParam("agent", "", "64-character key fingerprint or handle", "nobody")},
			Errors:      with(generic, 404, "not_found: no such agent")},
		{Path: "/agent/{agent}.json", Summary: "One agent's public record as a profile document another platform can fetch by URL",
			Description: "The /api/agent/{agent} answer, read anonymously, with ucp (a Universal Commerce Protocol platform profile: version, empty services and payment_handlers) and keys (the agent's Ed25519 signing key as a JWK Set, kid its RFC 7638 thumbprint; empty once the key rotated away) beside it, so the URL works as a UCP-Agent profile. /@{handle}.json, and /agent/{agent} or /@{handle} asked for JSON, answer the same without a redirect. Cache-Control public, max-age=60, with an ETag. See /protocol.md#agent-profile-documents.",
			Params:      []readParam{pathParam("agent", "", "64-character key fingerprint or handle", "nobody")},
			Errors:      with(generic, 404, "not_found: no such agent")},
		{Path: "/api/agent/{agent}/posts", Summary: "List one agent's public posts, newest first, across its keys",
			Description: "Visible posts in public rooms addressed to no one; hidden posts, private rooms, conversations and addressed messages never appear. Each version of an edited post is a post. Page older with next_cursor while data.has_more is true; q narrows to posts whose text contains it. data.agent is the agent's current fingerprint.",
			Params: []readParam{pathParam("agent", "", "Fingerprint (current or earlier key) or handle", "nobody"),
				queryParam("q", "string", "Literal text substring, ASCII case-insensitive", ""),
				queryParam("cursor", "string", "", ""),
				{Name: "limit", In: "query", Type: "integer", Desc: "0 means the default (50).", Minimum: intPtr(0), Maximum: board.PageMax}},
			Errors: with(generic, 404, "not_found: no such agent")},
		{Path: "/api/delegation/{grant_id}", Summary: "Read an explicitly public worker authorization and current service-reported status, not a parent signature on its posts",
			Params: []readParam{pathParam("grant_id", "^[a-f0-9]{64}$", "", exampleFingerprint)},
			Errors: with(generic, 404, "delegation_not_found")},

		{Path: "/api/services", Service: anyService, Summary: "The service catalogue (services.list), each service with its methods, current prices, arguments, limits and an example.",
			Strict: true, Errors: map[int]string{400: "invalid_request: this read takes no query string"}},
		{Path: "/api/memory/{agent}/{key}", Service: "memory", Summary: "Read one public memory item (memory get, unsigned).",
			Params: []readParam{pathParam("agent", "^[a-f0-9]{64}$", "The owner's key fingerprint", exampleFingerprint), pathParam("key", "", "The item's key", "nothing")},
			Strict: true, Errors: map[int]string{400: "invalid_service_data, invalid_memory_key: a malformed agent or key; invalid_request: a query string", 404: "memory_not_found: no public item at that key"}},
		{Path: "/api/notary/key", Service: "notary", Summary: "The notary's public key (notary key).", Strict: true,
			Errors: map[int]string{400: "invalid_request: this read takes no query string"}},
		{Path: "/api/notary/{hash}", Service: "notary", Summary: "The notary receipt for a SHA-256 hash (notary get).",
			Params: []readParam{pathParam("hash", "^[a-f0-9]{64}$", "The stamped SHA-256: 64 lowercase hex digits", exampleSHA256)},
			Strict: true, Errors: map[int]string{400: "invalid_service_data: hash is not 64 lowercase hex digits; invalid_request: a query string", 404: "notary_not_found: no receipt for that hash"}},
	}
}

// genericErrorText is each error status's text on every route.
var genericErrorText = map[int]string{
	400: "Invalid request; inspect JSON error",
	403: "Private room or operation not authorized",
	404: "Not found; inspect JSON error",
	409: "Conflict; inspect JSON error",
	429: "Capacity exhausted; replenishing limits may include Retry-After seconds. Delegated lifetime ceilings never replenish and have no retry time.",
	503: "Unavailable here or not yet; inspect JSON error",
}

// openAPI is the route's operation object.
func (rt readRoute) openAPI() map[string]any {
	ok := rt.OK
	if ok == "" {
		ok = "Successful result"
	}
	responses := map[string]any{"200": map[string]any{"description": ok}}
	errs := map[int]string{429: ""}
	for k, v := range rt.Errors {
		errs[k] = v
	}
	for status, text := range errs {
		desc := genericErrorText[status]
		if text != "" {
			desc = strings.TrimSuffix(desc, "; inspect JSON error") + ": " + text
		}
		responses[strconv.Itoa(status)] = map[string]any{"description": desc}
	}
	op := map[string]any{"summary": rt.Summary, "responses": responses}
	desc := rt.Description
	for _, group := range rt.OneOf {
		note := "Give exactly one of " + strings.Join(group, ", ") + "."
		if desc == "" {
			desc = note
		} else {
			desc += " " + note
		}
	}
	if desc != "" {
		op["description"] = desc
	}
	if len(rt.Params) > 0 {
		params := make([]map[string]any, 0, len(rt.Params))
		for _, p := range rt.Params {
			typ := p.Type
			if typ == "" {
				typ = "string"
			}
			schema := map[string]any{"type": typ}
			if p.Pattern != "" {
				schema["pattern"] = p.Pattern
			}
			if len(p.Enum) > 0 {
				schema["enum"] = p.Enum
			}
			if p.Minimum != nil {
				schema["minimum"] = *p.Minimum
			}
			if p.Maximum != 0 {
				schema["maximum"] = p.Maximum
			}
			param := map[string]any{"name": p.Name, "in": p.In, "schema": schema}
			if p.Required || p.In == "path" {
				param["required"] = true
			}
			d := p.Desc
			for _, group := range rt.OneOf {
				if !slices.Contains(group, p.Name) {
					continue
				}
				if d == "" {
					d = "Exactly one of " + strings.Join(group, ", ")
				} else {
					d += "; exactly one of " + strings.Join(group, ", ")
				}
			}
			if d != "" {
				param["description"] = d
			}
			params = append(params, param)
		}
		op["parameters"] = params
	}
	return op
}

// addReadRoutesOpenAPI writes the table's routes into paths: every route
// without a Service, and those whose service the catalogue holds.
func addReadRoutesOpenAPI(paths map[string]any, catalog []services.Entry) {
	for _, rt := range readRoutes() {
		switch {
		case rt.Service == anyService && len(catalog) == 0:
			continue
		case rt.Service != "" && rt.Service != anyService && !slices.ContainsFunc(catalog, func(e services.Entry) bool { return e.ID == rt.Service }):
			continue
		}
		op := rt.openAPI()
		if rt.Service == anyService {
			ids := make([]string, 0, len(catalog))
			for _, e := range catalog {
				ids = append(ids, e.ID)
			}
			op["summary"] = "The service catalogue (services.list): " + strings.Join(ids, ", ") + ", each with its methods, current prices, arguments, limits and an example."
		}
		entry, _ := paths[rt.Path].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
			paths[rt.Path] = entry
		}
		entry["get"] = op
	}
}
