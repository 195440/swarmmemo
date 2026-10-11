package board

// Offering discovery (RFC 0017): every 402 an offering answers carries the
// x402 v2 "bazaar" extension (how to call it: method, JSON body example and
// its schema, the response), so x402 directories can list it. A discovery
// probe (the keyless POST /@HANDLE/NAME with no input at all, no payment,
// from an offering whose schema needs input) answers the same 402 with a
// requirement that cannot be paid: its quote is offeringDiscoveryQuote,
// never an HMAC quote, so checkOfferingQuote refuses it before anything is
// verified. A payable quote is only minted for input the schema accepts,
// and binds that input's SHA-256 as before.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"swarmmemo/internal/services"
)

// offeringDiscoveryQuote is the quote of a discovery probe's requirement:
// not EXPIRES.SALT.MAC, so no payment can accept it.
const offeringDiscoveryQuote = "discovery-only"

// offeringBazaarBytes bounds the bazaar extension: x402 clients echo it in
// the payment payload, whose extensions are bounded (4 KiB) and whose header
// is at most services.TopupPaymentBytes.
const offeringBazaarBytes = 3 << 10

type discoveryProbeKey struct{}

// WithDiscoveryProbe marks an offering.buy from the keyless HTTP route
// without a payment: if its input is missing entirely and the offering's
// schema needs some, it answers the discovery 402 instead of
// invalid_offering_input.
func WithDiscoveryProbe(ctx context.Context) context.Context {
	return context.WithValue(ctx, discoveryProbeKey{}, true)
}

func discoveryProbe(ctx context.Context) bool {
	v, _ := ctx.Value(discoveryProbeKey{}).(bool)
	return v
}

// probeBuy says offering.buy's data carries no input at all (absent, null
// or {}) and no payment.
func probeBuy(raw string) bool {
	var d buyData
	if raw == "" || services.StrictObject([]byte(raw), &d) != nil || d.Schema.String() != "1" || d.Payment != "" {
		return false
	}
	in := strings.TrimSpace(string(d.Input))
	if in == "" || in == "null" {
		return true
	}
	var obj map[string]json.RawMessage
	return json.Unmarshal([]byte(in), &obj) == nil && obj != nil && len(obj) == 0
}

func isInvalidInput(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == "invalid_offering_input"
}

// offeringBazaar is the "bazaar" extension for o (x402 specs/extensions/
// bazaar.md, body method): info.input {type http, method POST, bodyType
// json, body: an example input}, info.output the 202's call, and the JSON
// Schema validating info, with the offering's input schema as body's.
// Within offeringBazaarBytes: descriptions go first, then the input schema.
func offeringBazaar(o offeringRow) map[string]any {
	var schemaMap map[string]any
	var sc inputSchema
	hasSchema := false
	if len(o.Listing.Input) > 0 {
		if parsed, err := parseInputSchema(o.Listing.Input); err == nil && json.Unmarshal(o.Listing.Input, &schemaMap) == nil {
			sc, hasSchema = parsed, true
			delete(schemaMap, "$schema")
		}
	}
	example := map[string]any{}
	if hasSchema {
		example = exampleInput(sc)
	}
	output := map[string]any{"ok": true, "data": map[string]any{"call": map[string]any{
		"call": "oc_CALL", "state": "authorized", "offering": map[string]any{"provider": o.Account, "name": o.Name, "rev": o.Rev},
		"amount": o.Listing.Price, "unit": "USDC", "pay_to": strings.ToLower(o.PayTo),
		"poll": pollPath(o.Account, o.Name, "oc_CALL", "SECRET"),
		"note": "202 Accepted; Location is the poll URL. GET it until state is answered (or declined, lapsed, refunded).",
	}}}
	build := func(body map[string]any, bodySchema map[string]any) map[string]any {
		return map[string]any{
			"info": map[string]any{
				"input":  map[string]any{"type": "http", "method": "POST", "bodyType": "json", "body": body},
				"output": map[string]any{"type": "json", "example": output},
			},
			"schema": map[string]any{
				"$schema": "https://json-schema.org/draft/2020-12/schema",
				"type":    "object",
				"properties": map[string]any{
					"input": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type":     map[string]any{"type": "string", "const": "http"},
							"method":   map[string]any{"type": "string", "enum": []string{"POST"}},
							"bodyType": map[string]any{"type": "string", "enum": []string{"json", "form-data", "text"}},
							"body":     bodySchema,
						},
						"required":             []string{"type", "method", "bodyType", "body"},
						"additionalProperties": false,
					},
					"output": map[string]any{
						"type":       "object",
						"properties": map[string]any{"type": map[string]any{"type": "string"}, "example": map[string]any{"type": "object"}},
						"required":   []string{"type"},
					},
				},
				"required": []string{"input"},
			},
		}
	}
	fits := func(v map[string]any) bool {
		raw, err := json.Marshal(v)
		return err == nil && len(raw) <= offeringBazaarBytes
	}
	if hasSchema {
		if ext := build(example, schemaMap); fits(ext) {
			return ext
		}
		if ext := build(example, withoutText(schemaMap)); fits(ext) {
			return ext
		}
	}
	return build(map[string]any{}, map[string]any{"type": "object"})
}

// withoutText is a copy of an input schema without its titles and
// descriptions.
func withoutText(schema map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range schema {
		if k == "title" || k == "description" {
			continue
		}
		out[k] = v
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		np := map[string]any{}
		for name, p := range props {
			if pm, ok := p.(map[string]any); ok {
				np[name] = withoutText(pm)
			} else {
				np[name] = p
			}
		}
		out["properties"] = np
	}
	return out
}

// exampleInput is an input sc accepts: every property it can fill (an
// enum's first value, "example" text at the length bounds, a number in
// range, true), or else the required ones only.
func exampleInput(sc inputSchema) map[string]any {
	names := make([]string, 0, len(sc.props))
	for name := range sc.props {
		names = append(names, name)
	}
	sort.Strings(names)
	required := map[string]bool{}
	for _, r := range sc.required {
		required[r] = true
	}
	for _, all := range []bool{true, false} {
		ex := map[string]any{}
		raw := map[string]json.RawMessage{}
		for _, name := range names {
			if !all && !required[name] {
				continue
			}
			if v, ok := exampleValue(sc.props[name]); ok {
				ex[name] = v
				raw[name], _ = json.Marshal(v)
			}
		}
		if sc.check(raw) == nil {
			return ex
		}
	}
	return map[string]any{}
}

func exampleValue(p inputProp) (any, bool) {
	if len(p.enum) > 0 {
		var v any
		return v, json.Unmarshal(p.enum[0], &v) == nil
	}
	switch p.typ {
	case "string":
		if p.minLength > OfferingInputBytes {
			return nil, false
		}
		s := "example"
		if n := p.minLength; int64(len(s)) < n {
			s = strings.Repeat("example ", int(n/8)+1)[:n]
		}
		if p.maxLength > 0 && int64(len(s)) > p.maxLength {
			s = s[:p.maxLength]
		}
		return s, true
	case "number", "integer":
		v := 1.0
		if p.maximum != nil && v > *p.maximum {
			v = *p.maximum
			if p.typ == "integer" {
				v = math.Floor(v)
			}
		}
		if p.minimum != nil && v < *p.minimum {
			v = *p.minimum
			if p.typ == "integer" {
				v = math.Ceil(v)
			}
		}
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return nil, false
		}
		return v, true
	case "boolean":
		return true, true
	}
	return nil, false
}

// OfferingsDirectoryMax bounds the offerings OfferingsDirectory lists, and
// with OfferingDirectoryTextBytes the size /openapi.json grows by.
const (
	OfferingsDirectoryMax      = 50
	OfferingDirectoryTextBytes = 280
)

// OfferingEntry is one active offering as /openapi.json lists it: public
// listing fields only (the provider's text, shortened, is untrusted).
type OfferingEntry struct {
	Path        string          // /@HANDLE/NAME (or /@ACCOUNT/NAME)
	Provider    string          // the handle, or the account without one
	Name        string          // the offering's name
	Title       string          // the listing's title
	Description string          // its description, at most OfferingDirectoryTextBytes
	Price       string          // canonical USDC, as the listing shows it
	Input       json.RawMessage // the input schema, if the listing has one
}

// OfferingsDirectory lists up to limit (at most OfferingsDirectoryMax)
// active offerings, retired ones never, in offering.list's order: offerings
// with paid calls in their record window first, then the newest. Nil while
// offerings are off.
func (s *Store) OfferingsDirectory(ctx context.Context, limit int) ([]OfferingEntry, error) {
	if !s.OfferingsEnabled() {
		return nil, nil
	}
	if limit <= 0 || limit > OfferingsDirectoryMax {
		limit = OfferingsDirectoryMax
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT "+offeringCols+" FROM offerings o WHERE state='active'"+
		` ORDER BY (SELECT count(*) FROM offering_calls c WHERE c.account=o.account AND c.name=o.name AND c.created_at>=? AND c.state IN ('paid','answered','overdue','refunded')) DESC, updated_at DESC, account, name LIMIT ?`,
		time.Now().Unix()-OfferingRecordDays*86400, limit)
	if err != nil {
		return nil, err
	}
	var list []offeringRow
	for rows.Next() {
		o, err := scanOffering(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, o)
	}
	if err = closeRows(rows); err != nil {
		return nil, err
	}
	out := make([]OfferingEntry, 0, len(list))
	for _, o := range list {
		_, handle, err := providerOf(ctx, tx, o.Account)
		if err != nil {
			return nil, err
		}
		provider := handle
		if provider == "" {
			provider = o.Account
		}
		e := OfferingEntry{Path: offeringPath(o.Account, handle, o.Name), Provider: provider, Name: o.Name,
			Title: shortText(o.Listing.Title), Description: shortText(o.Listing.Description), Price: o.Listing.Price}
		if len(o.Listing.Input) > 0 && json.Valid(o.Listing.Input) {
			e.Input = o.Listing.Input
		}
		out = append(out, e)
	}
	return out, nil
}

// shortText is s cut to OfferingDirectoryTextBytes on a rune boundary,
// with an ellipsis when cut.
func shortText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= OfferingDirectoryTextBytes {
		return s
	}
	cut := OfferingDirectoryTextBytes - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + "…"
}
