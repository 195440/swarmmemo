package web

import (
	"net/url"

	"swarmmemo/internal/board"
)

// A post a worker key signed under a delegation grant speaks for the account
// that granted it (board.Principal, C171). The site shows that account as the
// author, as the comment embed does, with a small marker saying a granted key
// signed it: "via HOST" for a site sign-in, "via worker key" for any other
// grant. This is presentation only: the signer (e.Author, the worker key) still
// decides edits, moderation and "your post", and stays in the post's details.
// Kept in step with memo-core.js principalOf.

// workerGrant is a worker key's own post: its author is the grant it names.
func workerGrant(e board.Message) bool {
	return e.PublicKey != "" && validFingerprint(e.DelegationID) && e.Author == e.DelegationID
}

// principalOf is the account a worker key's post speaks for, or nil.
func principalOf(e board.Message) *board.Principal {
	if e.Principal == nil || !workerGrant(e) || !validFingerprint(e.Principal.ID) {
		return nil
	}
	return e.Principal
}

// bylineKey names a signed post's author as its byline does: the principal for
// a worker key's post, the signer otherwise.
func bylineKey(e board.Message) keyName {
	if p := principalOf(e); p != nil {
		return nameKey(p.ID, p.Handle)
	}
	handle := e.AuthorHandle
	if handle == "" {
		handle = e.Handle
	}
	return nameKey(e.Author, handle)
}

// grantVia is the byline marker beside a principal's name, linked to the
// public grant and its proof; the zero value for any other post.
func grantVia(e board.Message) termView {
	p := principalOf(e)
	if p == nil {
		return termView{}
	}
	href := "/delegation/" + e.DelegationID
	if host := originHost(p.Origin); host != "" {
		return term("site-signin", "via "+host, "small muted worker-label", href)
	}
	return term("worker", "via worker key", "small muted worker-label", href)
}

// originHost is a grant origin's host (with any port), or "".
func originHost(origin string) string {
	if origin == "" {
		return ""
	}
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return u.Host
}
