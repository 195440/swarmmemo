package pay

// Bounded, strict JSON and base64 helpers both wires share. CheckStructure,
// DecodeBase64JSON and IsObject moved here from internal/services without
// change.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
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
	if err := CheckStructure(raw, JSONDepthMax); err != nil {
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
	if err := CheckStructure(raw, JSONDepthMax); err != nil {
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

// CheckStructure walks raw once: exactly one top-level object, no duplicate
// key in any object (case-insensitively), depth at most depthMax, nothing
// after the value. Moved from internal/services (data.go) without change
// but the depth bound, which is now a parameter.
func CheckStructure(raw []byte, depthMax int) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var stack []*frame
	first := true
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if first {
			if d, ok := tok.(json.Delim); !ok || d != '{' {
				return errStructure
			}
		} else if len(stack) == 0 {
			return errStructure // a second top-level value
		}
		first = false
		var top *frame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		if top != nil && top.object && top.key {
			if d, ok := tok.(json.Delim); ok && d == '}' {
				stack = stack[:len(stack)-1]
				if len(stack) > 0 {
					stack[len(stack)-1].valueDone()
				}
				continue
			}
			name, ok := tok.(string)
			// encoding/json matches field names case-insensitively, so keys
			// that differ only in case are duplicates too: "method" and
			// "METHOD" would let the last one win where another reader sees
			// the first (security review 1.20, L12).
			folded := strings.ToLower(strings.ToUpper(name))
			if !ok || top.keys[folded] {
				return errStructure
			}
			top.keys[folded] = true
			top.key = false
			continue
		}
		switch d := tok.(type) {
		case json.Delim:
			switch d {
			case '{', '[':
				if len(stack) >= depthMax {
					return errStructure
				}
				stack = append(stack, &frame{object: d == '{', keys: map[string]bool{}, key: d == '{'})
			case ']':
				stack = stack[:len(stack)-1]
				if len(stack) > 0 {
					stack[len(stack)-1].valueDone()
				}
			default:
				return errStructure
			}
		default:
			if top != nil {
				top.valueDone()
			}
		}
	}
	if first || len(stack) != 0 {
		return errStructure
	}
	return nil
}

// frame is one open object or array in CheckStructure.
type frame struct {
	object bool
	keys   map[string]bool
	key    bool // the next token in this object is a key
}

// valueDone records that a value ended inside f: in an object, a key is next.
func (f *frame) valueDone() {
	if f.object {
		f.key = true
	}
}
