// Package jsonstrict is the one structural check for JSON that two readers
// must read the same way: every command's data (internal/board), a service
// call's data and args (internal/services) and the payment wires
// (internal/pay). It decodes nothing; each caller decodes after it.
package jsonstrict

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// DepthMax is the nesting bound of a command and of its data (C493), the
// transport's bound on the command envelope.
const DepthMax = 32

// The refusals of Object and Value. Any other error is the decoder's syntax
// error inside the value.
var (
	ErrUTF8      = errors.New("jsonstrict: not valid UTF-8")
	ErrNotObject = errors.New("jsonstrict: not one JSON object")
	ErrDuplicate = errors.New("jsonstrict: duplicate key")
	ErrDepth     = errors.New("jsonstrict: nested too deep")
	ErrTrailing  = errors.New("jsonstrict: content after the value")
)

// Object walks raw once and refuses what lenient decoders read differently:
// invalid UTF-8, a value that is not one JSON object, a key repeated in any
// object at any depth (case-insensitively: encoding/json matches struct
// fields that way, so "method" and "METHOD" would let the last one win where
// another reader sees the first; security review 1.20, L12), nesting deeper
// than depthMax, and anything after the value.
func Object(raw []byte, depthMax int) error { return walk(raw, depthMax, true) }

// Value is Object for one JSON value of any type (a vote's data is a bare
// number). A syntax error inside the value comes back as the decoder's own
// error, so a caller can tell malformed JSON, which every parser refuses,
// from JSON two parsers could read differently (ErrUTF8, ErrDuplicate,
// ErrDepth, ErrTrailing).
func Value(raw []byte, depthMax int) error { return walk(raw, depthMax, false) }

// Ambiguous reports whether err is one of the refusals of JSON that parses
// but that two parsers could read differently.
func Ambiguous(err error) bool {
	return errors.Is(err, ErrUTF8) || errors.Is(err, ErrDuplicate) || errors.Is(err, ErrDepth) || errors.Is(err, ErrTrailing)
}

func walk(raw []byte, depthMax int, object bool) error {
	if !utf8.Valid(raw) {
		return ErrUTF8
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var stack []*frame
	first := true
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if !first && len(stack) == 0 {
			return ErrTrailing // anything after the one top-level value
		}
		if err != nil {
			return err
		}
		if first && object {
			if d, ok := tok.(json.Delim); !ok || d != '{' {
				return ErrNotObject
			}
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
			if !ok {
				return ErrNotObject
			}
			folded := strings.ToLower(strings.ToUpper(name))
			if top.keys[folded] {
				return ErrDuplicate
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
					return ErrDepth
				}
				stack = append(stack, &frame{object: d == '{', keys: map[string]bool{}, key: d == '{'})
			case ']':
				stack = stack[:len(stack)-1]
				if len(stack) > 0 {
					stack[len(stack)-1].valueDone()
				}
			default:
				return ErrNotObject
			}
		default:
			if top != nil {
				top.valueDone()
			}
		}
	}
	if first {
		return ErrNotObject
	}
	if len(stack) != 0 {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// frame is one open object or array in walk.
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
