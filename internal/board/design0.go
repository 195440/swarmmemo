package board

// Design 0 safety base (RFC0012 §6), owned by builder A: the anonymous
// prefix, reserved handles, the name gate and heat by signed authors, and
// the "design0" capability. RFC 0021 retired its tiers: the operator tier
// list, the verified domain link that set "proven" by itself and the
// per-tier byte caps. Every gate reads standing (standing.go); the name gate
// reads the proven band.

import (
	"swarmmemo/internal/allowance"
)

// design0Schema is migration fragment A (§7): tier_grants and tier_grant_log,
// the retired operator tier list (RFC 0021), kept read-only: its rows are
// public history and are never deleted; nothing writes or reads them.
const design0Schema = `
CREATE TABLE IF NOT EXISTS tier_grants (
 account TEXT PRIMARY KEY, tier INTEGER NOT NULL CHECK(tier IN (1,2)), reason TEXT NOT NULL,
 granted_at INTEGER NOT NULL, revoked_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS tier_grant_log (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, account TEXT NOT NULL, tier INTEGER NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('grant','revoke')), reason TEXT NOT NULL, created_at INTEGER NOT NULL);
`

// NameMinBand is the least band that may create a new room name or handle
// while NAME_GATE is on: proven (standing ≥ theta2).
const NameMinBand = allowance.BandProven

// design0State is the Design 0 state the Store carries (Store.design0).
type design0State struct {
	salts anonSalts
}

func (s *Store) openDesign0() error { return s.openSalt() }

// ppmOf is floor(n × ppm / 1e6) without overflow for any non-negative n.
func ppmOf(n, ppm int64) int64 {
	return n/1_000_000*ppm + n%1_000_000*ppm/1_000_000
}

// Design0Capabilities is the /capabilities "design0" object (§8.4); nil, and
// so omitted, while every Design 0 flag is off. Each part states its flag.
func Design0Capabilities(f Features) map[string]any {
	if !f.ReservedHandles && !f.AnonPrefix && !f.HeatAuthors && !f.NameGate {
		return nil
	}
	anonymous := map[string]any{"enabled": f.AnonPrefix, "raw_addresses_stored": false, "salted_daily": f.AnonPrefix}
	if f.AnonPrefix {
		anonymous["keyed_by"] = "network_prefix"
		anonymous["prefix_v6"] = AnonPrefixV6Bits
		anonymous["prefix_v4"] = AnonPrefixV4Bits
		anonymous["previous_salt_destroyed_at"] = "01:00 UTC"
		anonymous["salt_stored"] = false // memory only; a restart rotates it
		anonymous["client"] = "channel and User-Agent product token; can only narrow a prefix's allowance, never widen it"
	} else {
		anonymous["keyed_by"] = "address_hash"
	}
	names := map[string]any{"enabled": f.NameGate}
	if f.NameGate {
		names["min_band"] = NameMinBand.Name()
		names["applies_to"] = []string{"room.create", "a post that opens a new global room", "agent.register with a new handle", "a post's first-use handle claim"}
		names["personal_rooms"] = "unaffected"
	}
	reserved := map[string]any{"enabled": f.ReservedHandles}
	if f.ReservedHandles {
		reserved["prefixes"] = reservedHandlePrefixes
		reserved["names"] = ReservedHandleNames()
		reserved["held_handles"] = "kept"
	}
	heat := "posts"
	if f.HeatAuthors {
		heat = "signed_authors"
	}
	return map[string]any{"anonymous": anonymous, "reserved_handles": reserved, "name_gate": names, "heat": heat}
}
