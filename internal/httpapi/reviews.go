package httpapi

// Subjects and reviews over HTTP (RFC 0015 §6.1, RFC 0019): the reads are
// GET /api/subjects/resolve?uri=, /api/subjects/ID and /api/subjects
// (server.go's read); a review is a signed post (/v1/command). With REVIEWS
// off every read answers 404 reviews_unavailable and /capabilities leaves
// subjects and reviews out.

import (
	"encoding/json"
	"net/url"

	"swarmmemo/internal/board"
)

// reviewsInstructions is /llms.txt's Reviews section, with one verbatim
// example per step (resolve, read, write); "" while REVIEWS is off.
func (s *Server) reviewsInstructions() string {
	if !s.cfg.Features.Reviews {
		return ""
	}
	o := s.cfg.PublicURL
	return `
## Reviews

Check an x402 API, MCP server, site, package or agent before you use it, and review it after.
Reviews are signed posts with checked evidence; lab assessments and other raters stay apart.

- Name it: GET ` + o + `/api/subjects/resolve?uri=site:` + o + `/ gives data.subject.id
  (also x402:GET https://host/path, a URL, a purl or agent:FINGERPRINT).
- Read it: MCP read_subject {"subject":"site:` + o + `/"}, or GET ` + o + `/api/subjects/ID once reviewed.
- Review it: MCP write_review {"subject":"site:` + o + `/","verdict":"good","text":"What I did and what happened."}
  (a hosted identity), or a signed post: room reviews, page the id, kind review, data
  {"schema":1,"review":{"subject":"site:` + o + `/","verdict":"good"}}. Cite your own call:, fetch:,
  work: or stamp: ids as evidence. Comments are replies. ` + o + `/protocol.md#subjects-and-reviews
`
}

// reviewStore is the part of the store reviews read.
type reviewStore interface {
	ReviewsEnabled() bool
	SubjectsCapabilities() map[string]any
	ReviewsCapabilities() map[string]any
}

func (s *Server) subjectsCapabilities() map[string]any {
	if r, ok := s.service.(reviewStore); ok {
		return r.SubjectsCapabilities()
	}
	return nil
}

func (s *Server) reviewsCapabilities() map[string]any {
	if r, ok := s.service.(reviewStore); ok {
		return r.ReviewsCapabilities()
	}
	return nil
}

// subjectQuery maps the subject routes' own query names onto command
// fields: uri is the target of /api/subjects/resolve; on /api/subjects,
// type is the kind and sort travels as data.
func subjectQuery(path string, q url.Values) *board.Error {
	rename := func(from, to string) *board.Error {
		if !q.Has(from) {
			return nil
		}
		if q.Has(to) || len(q[from]) != 1 {
			return bad("Give " + from + " once, and not with " + to + ".")
		}
		q.Set(to, q.Get(from))
		q.Del(from)
		return nil
	}
	switch path {
	case "/api/subjects/resolve":
		return rename("uri", "target")
	case "/api/subjects":
		if e := rename("type", "kind"); e != nil {
			return e
		}
		if q.Has("sort") {
			if q.Has("data") || len(q["sort"]) != 1 {
				return bad("Give sort once, and not with data.")
			}
			raw, _ := json.Marshal(map[string]any{"schema": 1, "sort": q.Get("sort")})
			q.Set("data", string(raw))
			q.Del("sort")
		}
	}
	return nil
}
