package jsonstrict

import (
	"errors"
	"strings"
	"testing"
)

func TestObjectAndValue(t *testing.T) {
	nest := func(n int) string { return strings.Repeat(`[`, n) + strings.Repeat(`]`, n) }
	for _, tc := range []struct {
		raw         string
		object, val error // nil: accepted; errSyntax: the decoder's own error
	}{
		{`{"a":{"b":[1,{"c":2}]}}`, nil, nil},
		{`{"a":1,"A":2}`, ErrDuplicate, ErrDuplicate},
		{`{"a":[{"x":1,"x":1}]}`, ErrDuplicate, ErrDuplicate},
		{`{"a":{"x":1},"b":{"x":1}}`, nil, nil},
		{`{} {}`, ErrTrailing, ErrTrailing},
		{`{} x`, ErrTrailing, ErrTrailing},
		{`1`, ErrNotObject, nil},
		{`1 2`, ErrNotObject, ErrTrailing},
		{"{\"a\":\"\xff\"}", ErrUTF8, ErrUTF8},
		{`{"a":` + nest(31) + `}`, nil, nil},
		{`{"a":` + nest(32) + `}`, ErrDepth, ErrDepth},
		{`{"a":`, errSyntax, errSyntax},
		{``, ErrNotObject, ErrNotObject},
	} {
		for _, c := range []struct {
			got, want error
		}{{Object([]byte(tc.raw), DepthMax), tc.object}, {Value([]byte(tc.raw), DepthMax), tc.val}} {
			switch {
			case c.want == errSyntax:
				if c.got == nil || Ambiguous(c.got) {
					t.Errorf("%q: %v, want a syntax error", tc.raw, c.got)
				}
			case !errors.Is(c.got, c.want) && c.got != c.want:
				t.Errorf("%q: %v, want %v", tc.raw, c.got, c.want)
			}
		}
	}
}

var errSyntax = errors.New("syntax")
