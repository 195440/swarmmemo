package pay

// Bounded, strict JSON and base64 helpers both wires share. DecodeBase64JSON
// and IsObject moved here from internal/services without change; the
// structural check is internal/jsonstrict's, shared with command data.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"swarmmemo/internal/jsonstrict"
)

// JSONDepthMax bounds the nesting of any JSON a wire parses
// (services.JSONDepthMax is this value).
const JSONDepthMax = 16

// strictObject decodes one JSON object into dst and refuses what a lenient
// decoder accepts silently: invalid UTF-8, unknown fields, duplicate keys
// (case-insensitively), nesting beyond JSONDepthMax, a value that is not an
// object, trailing data. It accepts exactly what services.StrictObject
// accepts; only the error values differ (a wire maps every refusal to one
// code).
func strictObject(raw []byte, dst any) error {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return errStructure
	}
	if err := jsonstrict.Object(raw, JSONDepthMax); err != nil {
		return errStructure
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	return dec.Decode(dst)
}

// lenientObject is strictObject that ignores unknown fields, which the MPP
// core spec requires ("implementations MUST ignore unknown parameters and
// fields"). Duplicate keys, depth, UTF-8 and trailing data are still
// refused, so no field can be read two ways.
func lenientObject(raw []byte, dst any) error {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return errStructure
	}
	if err := jsonstrict.Object(raw, JSONDepthMax); err != nil {
		return errStructure
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(dst)
}

// DecodeBase64JSON decodes s, at most max bytes, in any of the four base64
// alphabets and paddings x402 clients use (moved from services without
// change; max was services' x402HeaderBytes).
func DecodeBase64JSON(s string, max int) ([]byte, bool) {
	if s == "" || len(s) > max {
		return nil, false
	}
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, true
		}
	}
	return nil, false
}

// IsObject says whether raw starts a JSON object (moved from services).
func IsObject(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 1 && raw[0] == '{'
}

var errStructure = errors.New("pay: malformed JSON structure")
