package proofs

// The safe fetcher every provider calls through: one HTTP client that dials
// only public addresses (safenet, after resolution, so a rebinding resolver
// cannot turn it inward), follows no redirect, bounds every answer and time,
// sends no cookie and never logs a URL, a body or a header. Tests point it at
// an httptest server with Config.Client and Config.TestBaseURL.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"swarmmemo/internal/safenet"
)

const (
	fetchTimeout = 8 * time.Second
	// fetchBodyMax bounds an answer; bigFetchBodyMax an npm packument.
	fetchBodyMax    = 1 << 20
	bigFetchBodyMax = 48 << 20
	userAgent       = "swarmmemo-proofs (+https://swarmmemo.com/protocol.md#raise-your-standing)"
)

// SafeClient is the default client: public addresses only, no redirects,
// no proxy, bounded timeouts.
func SafeClient() *http.Client {
	return &http.Client{
		Timeout:       fetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return safenet.Dial(ctx, network, addr, fetchTimeout)
			},
			TLSHandshakeTimeout:   fetchTimeout,
			ResponseHeaderTimeout: fetchTimeout,
			MaxIdleConns:          4,
			IdleConnTimeout:       60 * time.Second,
		},
	}
}

// fetcher is a provider's client and its test rewrite.
type fetcher struct {
	client *http.Client
	// testBase, when set, replaces every https://HOST of a provider URL
	// with testBase + "/HOST" (tests only).
	testBase string
}

// u is a provider URL, rewritten for a test server.
func (f fetcher) u(raw string) string {
	if f.testBase == "" {
		return raw
	}
	if rest, ok := strings.CutPrefix(raw, "https://"); ok {
		return strings.TrimRight(f.testBase, "/") + "/" + rest
	}
	return raw
}

// request is one call.
type request struct {
	method  string
	url     string
	header  http.Header
	form    url.Values // a POST form body
	body    []byte     // a JSON body
	limit   int64
	into    any
	allow4x bool // a 4xx other than 404 is a definite failure, not unavailable
}

// errTooLarge is an answer past its bound.
var errTooLarge = errors.New("proofs: answer too large")

// do runs one request and decodes a 200 JSON answer into r.into: ErrFailed
// for a definite refusal (404, or any 4xx when allow4x), ErrUnavailable for
// anything inconclusive (network, timeout, 429, 5xx, undecodable).
func (f fetcher) do(ctx context.Context, r request) error {
	var body io.Reader
	switch {
	case r.form != nil:
		body = strings.NewReader(r.form.Encode())
	case r.body != nil:
		body = bytes.NewReader(r.body)
	}
	if r.method == "" {
		r.method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, r.method, f.u(r.url), body)
	if err != nil {
		return ErrFailed
	}
	for k, vs := range r.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("User-Agent", userAgent)
	switch {
	case r.form != nil:
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	case r.body != nil:
		req.Header.Set("Content-Type", "application/json")
	}
	client := f.client
	if client == nil {
		client = SafeClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	limit := r.limit
	if limit <= 0 {
		limit = fetchBodyMax
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return ErrUnavailable
	}
	if int64(len(raw)) > limit {
		return errTooLarge
	}
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrFailed
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return ErrUnavailable
	case resp.StatusCode >= 400 && resp.StatusCode < 500 && r.allow4x:
		return ErrFailed
	default:
		return ErrUnavailable
	}
	if r.into == nil {
		return nil
	}
	if json.Unmarshal(raw, r.into) != nil {
		return ErrUnavailable
	}
	return nil
}

// bearer is an Authorization header with an access token.
func bearer(token string) http.Header { return http.Header{"Authorization": {"Bearer " + token}} }
