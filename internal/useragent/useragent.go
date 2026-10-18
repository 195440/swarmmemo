// Package useragent names SwarmMemo's outbound HTTP requests. A request
// that sets no User-Agent goes out as the library default
// (Go-http-client/1.1), which Cloudflare's Browser Integrity Check answers
// with 403 "error code: 1010"; a named one passes.
package useragent

// For is the User-Agent of a component's requests:
// "SwarmMemo/<component> (+https://swarmmemo.com)".
func For(component string) string {
	return "SwarmMemo/" + component + " (+https://swarmmemo.com)"
}
