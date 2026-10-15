package web

// Verified by SwarmMemo (C353): /v/ID shows one signed verification
// (board.Verification), /v/ID/badge.svg its README badge, /v/ID.png its card,
// /v/badge?repo=URL&commit=SHA (or file=SHA256) the newest record's badge for
// a subject and /v/latest?… a redirect to that record's page. A record never
// changes once published (a moderation hide takes it down), so the page and
// its badge are cached; the subject badge follows new records within
// minutes. The badge is the C200 renderer's (badge.go): only the board's own
// words and numbers, escaped.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"swarmmemo/internal/board"
	"swarmmemo/internal/cards"
)

const (
	// VerifySubjectBadgePath and VerifyLatestPath are the subject's badge
	// and the redirect to its newest record.
	VerifySubjectBadgePath = board.VerificationPath + "badge"
	VerifyLatestPath       = board.VerificationPath + "latest"
	// VerifyPageCacheControl is a record page's caching: it shows the
	// verifier's standing, which a trust run moves, so five minutes.
	VerifyPageCacheControl = "public, max-age=300, stale-while-revalidate=3600"
	// VerifyBadgeCacheControl is a record's own badge: its numbers never
	// change, so an hour, revalidated in the background for a day.
	VerifyBadgeCacheControl = "public, max-age=3600, stale-while-revalidate=86400"
	// verifyCacheTTL is how long a drawn badge or card is reused in memory,
	// so a hide takes effect within it.
	verifyCacheTTL = time.Minute
	verifyCacheMax = 1024
)

// verifyCache keeps drawn badges and cards: serving one is a map read.
var verifyCache = struct {
	sync.Mutex
	m map[string]verifyCached
}{m: map[string]verifyCached{}}

type verifyCached struct {
	body    []byte
	expires time.Time
	missing bool
}

func cachedVerify(key string, now time.Time) (verifyCached, bool) {
	verifyCache.Lock()
	defer verifyCache.Unlock()
	c, ok := verifyCache.m[key]
	return c, ok && now.Before(c.expires)
}

func keepVerify(key string, c verifyCached, now time.Time) {
	verifyCache.Lock()
	defer verifyCache.Unlock()
	if len(verifyCache.m) >= verifyCacheMax {
		for k, v := range verifyCache.m {
			if !now.Before(v.expires) {
				delete(verifyCache.m, k)
			}
		}
		if len(verifyCache.m) >= verifyCacheMax {
			verifyCache.m = map[string]verifyCached{}
		}
	}
	c.expires = now.Add(verifyCacheTTL)
	verifyCache.m[key] = c
}

// verifyBadgeFacts is what a record's badge says.
func verifyBadgeFacts(v *board.Verification) badgeFacts {
	if v == nil {
		return badgeFacts{Label: "verified by SwarmMemo", Value: "no record", Tone: "none", Spoken: "Verified by SwarmMemo: no record for this subject"}
	}
	count := fmt.Sprintf("%d/%d checks passed", v.Passed, v.Ran)
	if v.Failed > 0 {
		return badgeFacts{Label: "verified by SwarmMemo", Value: fmt.Sprintf("failed · %d/%d · %s", v.Passed, v.Ran, v.ShortHash()), Tone: "bad",
			Spoken: "Verified by SwarmMemo: failed, " + count + ", at " + v.ShortHash()}
	}
	return badgeFacts{Label: "verified by SwarmMemo", Value: count + " · " + v.ShortHash(), Tone: "good", Spoken: "Verified by SwarmMemo: " + count + ", at " + v.ShortHash()}
}

// verificationOf reads a verify.get or verify.list answer's record(s).
func verificationOf(res board.Result) *board.Verification {
	if v, ok := res.Data["verification"].(board.Verification); ok {
		return &v
	}
	if list, ok := res.Data["verifications"].([]board.Verification); ok && len(list) > 0 {
		return &list[0]
	}
	return nil
}

// verifySubjectCommand is verify.list's command for /v/badge and /v/latest:
// repo (with an optional commit) or file.
func verifySubjectCommand(q url.Values) (board.Command, bool) {
	c := board.Command{Operation: "verify.list", Limit: 1}
	switch {
	case q.Get("repo") != "" && q.Get("file") == "":
		c.Target, c.Query = q.Get("repo"), q.Get("commit")
	case q.Get("file") != "" && q.Get("repo") == "" && q.Get("commit") == "":
		c.Target = q.Get("file")
	default:
		return c, false
	}
	return c, true
}

// verifyRoute serves the badges, the card and the redirect; ok is false
// for every other path (the page is web.go's view switch).
func verifyRoute(w http.ResponseWriter, r *http.Request, execute func(board.Command) (board.Result, error)) bool {
	p := r.URL.Path
	if !strings.HasPrefix(p, board.VerificationPath) {
		return false
	}
	now := time.Now()
	switch {
	case p == VerifySubjectBadgePath || p == VerifyLatestPath:
		c, ok := verifySubjectCommand(r.URL.Query())
		if !ok {
			http.Error(w, "Name the subject: ?repo=https://host/owner/repo (and optionally &commit=FULL_SHA), or ?file=SHA256.", http.StatusBadRequest)
			return true
		}
		// A README's badge is fetched on every view: the drawn SVG is kept
		// a minute per subject.
		key := "subject:" + c.Target + "|" + c.Query
		if cached, hit := cachedVerify(key, now); hit && p == VerifySubjectBadgePath {
			serveBadge(w, r, cached.body, BadgeCacheControl)
			return true
		}
		res, err := execute(c)
		var be *board.Error
		if errors.As(err, &be) && be.Status < 500 {
			http.Error(w, be.Message, be.Status)
			return true
		}
		if err != nil {
			http.Error(w, "Verifications are temporarily unavailable.", http.StatusServiceUnavailable)
			return true
		}
		v := verificationOf(res)
		if p == VerifyLatestPath {
			if v == nil {
				http.Error(w, "No verification names this subject yet.", http.StatusNotFound)
				return true
			}
			w.Header().Set("Cache-Control", BadgeCacheControl)
			http.Redirect(w, r, board.VerificationPath+v.ID, http.StatusFound)
			return true
		}
		body := badgeSVG(verifyBadgeFacts(v))
		keepVerify(key, verifyCached{body: body}, now)
		serveBadge(w, r, body, BadgeCacheControl)
		return true
	case strings.HasSuffix(p, "/badge.svg"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, board.VerificationPath), "/badge.svg")
		if !board.ValidVerificationID(id) {
			http.NotFound(w, r)
			return true
		}
		c, hit := cachedVerify("badge:"+id, now)
		if !hit {
			res, err := execute(board.Command{Operation: "verify.get", Target: id})
			var be *board.Error
			switch {
			case err == nil:
				c = verifyCached{body: badgeSVG(verifyBadgeFacts(verificationOf(res)))}
			case errors.As(err, &be) && be.Status < 500:
				c = verifyCached{missing: true}
			default:
				http.Error(w, "Badges are temporarily unavailable.", http.StatusServiceUnavailable)
				return true
			}
			keepVerify("badge:"+id, c, now)
		}
		if c.missing {
			http.NotFound(w, r)
			return true
		}
		serveBadge(w, r, c.body, VerifyBadgeCacheControl)
		return true
	case strings.HasSuffix(p, ".png"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, board.VerificationPath), ".png")
		if !board.ValidVerificationID(id) {
			http.NotFound(w, r)
			return true
		}
		c, hit := cachedVerify("card:"+id, now)
		if !hit {
			res, err := execute(board.Command{Operation: "verify.get", Target: id})
			var be *board.Error
			switch {
			case err == nil:
				png, err := cards.DrawPNG(verifyCard(verificationOf(res)))
				if err != nil {
					http.Error(w, "The card could not be drawn.", http.StatusServiceUnavailable)
					return true
				}
				c = verifyCached{body: png}
			case errors.As(err, &be) && be.Status < 500:
				c = verifyCached{missing: true}
			default:
				http.Error(w, "Cards are temporarily unavailable.", http.StatusServiceUnavailable)
				return true
			}
			keepVerify("card:"+id, c, now)
		}
		if c.missing {
			http.NotFound(w, r)
			return true
		}
		h := w.Header()
		h.Set("Content-Type", "image/png")
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		h.Set("Cache-Control", VerifyBadgeCacheControl)
		sum := sha256.Sum256(c.body)
		h.Set("ETag", `"`+hex.EncodeToString(sum[:8])+`"`)
		http.ServeContent(w, r, "verification.png", time.Time{}, bytes.NewReader(c.body))
		return true
	}
	return false
}

// serveBadge writes an SVG badge with its caching, ETag and the strict
// headers every badge carries (badgeRoute's).
func serveBadge(w http.ResponseWriter, r *http.Request, body []byte, cache string) {
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`
	h := w.Header()
	h.Set("Content-Type", "image/svg+xml; charset=utf-8")
	h.Set("Cache-Control", cache)
	h.Set("ETag", etag)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// verifyCard is a record's 1200x630 card.
func verifyCard(v *board.Verification) cards.Card {
	result := fmt.Sprintf("%d of %d checks passed", v.Passed, v.Ran)
	if v.Failed > 0 {
		result = fmt.Sprintf("Failed: %d of %d checks passed", v.Passed, v.Ran)
	}
	var lines []string
	for _, c := range v.Checks {
		line := c.State + "  " + c.Property
		if c.Note != "" {
			line += ": " + c.Note
		}
		lines = append(lines, line)
	}
	return cards.Card{Kind: "v", Key: v.ID, Heading: "Verified by SwarmMemo", Title: v.SubjectLabel() + ": " + result,
		Meta: "by " + verifierName(v) + " · " + time.Unix(v.At, 0).UTC().Format("2006-01-02") + " · $ " + cards.OneLine(v.Command, 80),
		Body: strings.Join(lines, "\n"), Footer: "swarmmemo.com" + board.VerificationPath + v.ID}
}

func verifierName(v *board.Verification) string {
	if v.Verifier.Handle != "" {
		return "@" + v.Verifier.Handle
	}
	return v.Verifier.Fingerprint[:12]
}

// verifyPage is /v/ID.
type verifyPage struct {
	V            board.Verification
	Subject      string
	Verifier     string
	Standing     string
	Rerun        string
	SigCheck     string
	NotaryCheck  string
	Badge        string
	Markdown     string
	HTML         string
	SubjectBadge string
	Latest       string
}

// loadVerification fills p from verify.get; the status is the page's.
func loadVerification(r *http.Request, p *page, execute func(board.Command) (board.Result, error)) int {
	id := strings.TrimPrefix(r.URL.Path, board.VerificationPath)
	missing := func(status int, notice string) int {
		p.View, p.NoIndex, p.Title, p.Notice = "missing", true, "Verification not found", notice
		return status
	}
	if !board.ValidVerificationID(id) {
		return missing(404, "")
	}
	res, err := execute(board.Command{Operation: "verify.get", Target: id})
	if err != nil {
		var be *board.Error
		if errors.As(err, &be) && be.Status < 500 {
			return missing(be.Status, be.Message)
		}
		return missing(503, "Verifications are temporarily unavailable.")
	}
	v := verificationOf(res)
	if v == nil {
		return missing(503, "Verifications are temporarily unavailable.")
	}
	vp := &verifyPage{V: *v, Subject: v.SubjectLabel(), Verifier: verifierName(v), Badge: board.VerificationPath + v.ID + "/badge.svg",
		Markdown: board.BadgeMarkdown(v.URL), HTML: board.BadgeHTML(v.URL)}
	if s := v.Verifier.StandingCents; s != nil {
		vp.Standing = "no standing yet"
		if *s > 0 {
			vp.Standing = fmt.Sprintf("standing $%d.%02d", *s/100, *s%100)
			if v.Verifier.Band != "" {
				vp.Standing += ", " + v.Verifier.Band + " band"
			}
		}
	}
	subject := "file=" + v.FileSHA256
	if v.Repo != "" {
		subject = "repo=" + url.QueryEscape(v.Repo) + "&commit=" + v.Commit
	}
	vp.Latest = VerifyLatestPath + "?" + subject
	vp.SubjectBadge = "[![Verified by SwarmMemo](" + siteOrigin + VerifySubjectBadgePath + "?" + subject + ")](" + siteOrigin + vp.Latest + ")"
	vp.Rerun = rerunBlock(v)
	vp.SigCheck = sigCheckBlock(v)
	if v.Notary != nil {
		vp.NotaryCheck = "curl -sO " + siteOrigin + "/clients/python/verify_log.py\npython3 verify_log.py notary " + v.RecordSHA256
	}
	p.View, p.VerifyView = "verification", vp
	p.Title = "Verified by SwarmMemo: " + vp.Subject
	verdict := fmt.Sprintf("%d of %d checks passed", v.Passed, v.Ran)
	if v.Failed > 0 {
		verdict = fmt.Sprintf("failed, %d of %d checks passed", v.Passed, v.Ran)
	}
	p.Description = vp.Subject + ": " + verdict + ", signed by " + vp.Verifier + ". The command, environment, checks and what was not tested, with how to re-run it."
	p.Canonical = board.VerificationPath + v.ID
	p.OG = &openGraph{Type: "article", Title: p.Title, Description: p.Description, URL: p.Canonical, Image: p.Canonical + ".png", Card: true}
	return 200
}

// rerunBlock is the shell a reader runs to repeat the record.
func rerunBlock(v *board.Verification) string {
	var b strings.Builder
	if v.Repo != "" {
		dir := v.Repo[strings.LastIndex(v.Repo, "/")+1:]
		if dir == "" || strings.ContainsAny(dir, "%'\"$`\\") {
			dir = "repo"
		}
		fmt.Fprintf(&b, "git clone %s %s && cd %s\ngit checkout %s\n", v.Repo, dir, dir, v.Commit)
	}
	if v.FileSHA256 != "" {
		name := v.File
		if name == "" {
			name = "FILE"
		}
		fmt.Fprintf(&b, "sha256sum %s   # must print %s\n", strconv.Quote(name), v.FileSHA256)
	}
	b.WriteString(v.Command + "\n")
	fmt.Fprintf(&b, "# The verifier's output hashed to %s.\n# Output with times or paths will differ: compare the checks.", v.OutputSHA256)
	return b.String()
}

// sigCheckBlock checks the record offline: the verifier's signature over
// its signed command, and that command's hash.
func sigCheckBlock(v *board.Verification) string {
	return `curl -s ` + siteOrigin + `/api/verifications/` + v.ID + ` > v.json
python3 - <<'EOF'
import base64, hashlib, json
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
v = json.load(open("v.json"))["data"]["verification"]
b = lambda s: base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))
key, payload = b(v["verifier"]["public_key"]), v["signed_payload"].encode()
Ed25519PublicKey.from_public_bytes(key).verify(b(v["signature"]), payload)
assert hashlib.sha256(key).hexdigest() == v["verifier"]["fingerprint"]
assert hashlib.sha256(payload).hexdigest() == v["record_sha256"]
print("signed by", v["verifier"]["fingerprint"])
EOF`
}

// verifyStateLabel is a check state in words.
func verifyStateLabel(state string) string {
	return map[string]string{"pass": "pass", "fail": "fail", "not_checked": "not checked", "not_checkable": "not checkable"}[state]
}
