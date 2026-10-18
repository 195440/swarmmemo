// Package b64 is SwarmMemo's one base64 rule (docs/PROTOCOL.md, Encodings):
// a value decodes only when it is exactly the canonical encoding of its bytes,
// so two verifiers can never split on the same input. Keys, signatures,
// proofs, receipts, nonces, cursors and data use unpadded base64url (URL); the
// transparency log's C2SP fields use padded standard base64 (Std). Anything
// else (padding on a base64url value, a missing pad, the other alphabet, a line
// break or space, unused trailing bits set) is refused.
//
// Every decoder in the repository passes testdata/base64_vectors.json.
package b64

import (
	"encoding/base64"
	"errors"
)

var (
	// ErrURL is why a base64url value was refused.
	ErrURL = errors.New("not canonical unpadded base64url: use only A-Z a-z 0-9 - _, no = padding, no spaces or line breaks, and leave unused trailing bits zero (re-encoding the bytes must give back the same text)")
	// ErrStd is why a standard base64 value was refused.
	ErrStd = errors.New("not canonical padded base64: use only A-Z a-z 0-9 + /, pad with = to a multiple of 4, no spaces or line breaks, and leave unused trailing bits zero (re-encoding the bytes must give back the same text)")
)

// URL decodes canonical unpadded base64url (RFC 4648 section 5).
func URL(s string) ([]byte, error) {
	return decode(base64.RawURLEncoding, s, ErrURL)
}

// Std decodes canonical padded standard base64 (RFC 4648 section 4).
func Std(s string) ([]byte, error) {
	return decode(base64.StdEncoding, s, ErrStd)
}

// URLSize decodes canonical unpadded base64url of exactly n bytes.
func URLSize(s string, n int) ([]byte, error) {
	raw, err := URL(s)
	if err == nil && len(raw) != n {
		return nil, errors.New("wrong length for this field")
	}
	return raw, err
}

// decode refuses what encoding/base64 tolerates: it skips \r and \n, and
// without Strict accepts nonzero trailing bits. The re-encoding comparison is
// the rule; Strict only fails fast.
func decode(enc *base64.Encoding, s string, refused error) ([]byte, error) {
	raw, err := enc.Strict().DecodeString(s)
	if err != nil || enc.EncodeToString(raw) != s {
		return nil, refused
	}
	return raw, nil
}
