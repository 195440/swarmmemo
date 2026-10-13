package safenet

// The one list of address ranges no outbound request may reach. Every
// consumer reads it from here: PublicIP below (webhooks, service upstreams,
// OAuth metadata), moderation.ClassifyAddr (run egress), references.publicIP
// (reference URLs), and the runs loader, whose
// workers/runs-loader/src/reserved.gen.ts is generated from it
// (go generate ./internal/safenet; TestReservedTSIsCurrent fails when stale).
//
// It is the union of the IANA IPv4 and IPv6 Special-Purpose Address
// Registries (RFC 6890 and its updates), the multicast blocks, and the
// deprecated ranges that still embed or alias an internal address. Blocking a
// range IANA marks globally reachable (an anycast service, AS112, ORCHIDv2)
// costs nothing here: no feature has a reason to dial one by address literal.

//go:generate go test -run TestReservedTSIsCurrent -update-reserved-ts .

import "net/netip"

// ReservedRange is one blocked prefix and the registry entry it comes from.
type ReservedRange struct {
	CIDR   string
	Source string
}

// ReservedRanges is the list, in registry order.
var ReservedRanges = []ReservedRange{
	// IPv4 Special-Purpose Address Registry, plus multicast.
	{"0.0.0.0/8", "RFC 791: this network"},
	{"10.0.0.0/8", "RFC 1918: private use"},
	{"100.64.0.0/10", "RFC 6598: shared address space (carrier-grade NAT)"},
	{"127.0.0.0/8", "RFC 1122: loopback"},
	{"169.254.0.0/16", "RFC 3927: link local (cloud metadata lives here)"},
	{"172.16.0.0/12", "RFC 1918: private use"},
	{"192.0.0.0/24", "RFC 6890: IETF protocol assignments (incl. DS-Lite, NAT64 discovery, Oracle metadata 192.0.0.192)"},
	{"192.0.2.0/24", "RFC 5737: documentation (TEST-NET-1)"},
	{"192.31.196.0/24", "RFC 7535: AS112-v4"},
	{"192.52.193.0/24", "RFC 7450: AMT"},
	{"192.88.99.0/24", "RFC 7526: deprecated 6to4 relay anycast"},
	{"192.168.0.0/16", "RFC 1918: private use"},
	{"192.175.48.0/24", "RFC 7534: direct delegation AS112 service"},
	{"198.18.0.0/15", "RFC 2544: benchmarking"},
	{"198.51.100.0/24", "RFC 5737: documentation (TEST-NET-2)"},
	{"203.0.113.0/24", "RFC 5737: documentation (TEST-NET-3)"},
	{"224.0.0.0/4", "RFC 5771: multicast"},
	{"240.0.0.0/4", "RFC 1112: reserved, and 255.255.255.255 limited broadcast (RFC 919)"},

	// IPv6 Special-Purpose Address Registry, plus multicast and deprecated forms.
	{"::/128", "RFC 4291: unspecified address"},
	{"::1/128", "RFC 4291: loopback"},
	{"::/96", "RFC 4291: deprecated IPv4-compatible addresses (embed an IPv4 address)"},
	{"::ffff:0:0/96", "RFC 4291: IPv4-mapped (consumers unmap first, so the IPv4 ranges apply)"},
	{"::ffff:0:0:0/96", "RFC 2765: IPv4-translated (SIIT)"},
	{"64:ff9b::/96", "RFC 6052: IPv4/IPv6 translation (NAT64 well-known prefix)"},
	{"64:ff9b:1::/48", "RFC 8215: local-use IPv4/IPv6 translation"},
	{"100::/64", "RFC 6666: discard-only"},
	{"100:0:0:1::/64", "RFC 9780: dummy IPv6 prefix"},
	{"2001::/23", "RFC 2928: IETF protocol assignments (covers Teredo 2001::/32, benchmarking 2001:2::/48, ORCHID 2001:10::/28, ORCHIDv2 2001:20::/28, AMT, AS112-v6, DNS-SD SRP and drone DET anycasts)"},
	{"2001::/32", "RFC 4380: Teredo (embeds an IPv4 address; inside 2001::/23, listed for the embedded-address check)"},
	{"2001:db8::/32", "RFC 3849: documentation"},
	{"2002::/16", "RFC 3056: 6to4 (embeds an IPv4 address)"},
	{"2620:4f:8000::/48", "RFC 7534: direct delegation AS112 service"},
	{"3fff::/20", "RFC 9637: documentation"},
	{"5f00::/16", "RFC 9602: segment routing (SRv6) SIDs"},
	{"fc00::/7", "RFC 4193: unique local (AWS metadata fd00:ec2::254 lives here)"},
	{"fe80::/10", "RFC 4291: link-local unicast"},
	{"fec0::/10", "RFC 3879: deprecated site-local"},
	{"ff00::/8", "RFC 4291: multicast"},
}

// Reserved is ReservedRanges parsed: what PublicIP, moderation and references
// test addresses against.
var Reserved = func() []netip.Prefix {
	out := make([]netip.Prefix, len(ReservedRanges))
	for i, r := range ReservedRanges {
		out[i] = netip.MustParsePrefix(r.CIDR)
	}
	return out
}()

// IsReserved reports whether a is inside a reserved range. Zones are dropped
// and an IPv4-mapped address is unwrapped first, so ::ffff:127.0.0.1 meets the
// IPv4 loopback range.
func IsReserved(a netip.Addr) bool {
	a = a.WithZone("").Unmap()
	for _, p := range Reserved {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ReservedSamples is, for every reserved range, its first, last and a middle
// address (and each IPv4 one's IPv4-mapped IPv6 form): what each consumer's
// test asserts it refuses.
func ReservedSamples() []netip.Addr {
	var out []netip.Addr
	for _, p := range Reserved {
		first := p.Addr()
		b := first.AsSlice()
		last := make([]byte, len(b))
		mid := make([]byte, len(b))
		copy(last, b)
		copy(mid, b)
		for bit := p.Bits(); bit < len(b)*8; bit++ {
			last[bit/8] |= 0x80 >> (bit % 8)
			if bit > p.Bits() { // the top host bit clear, the rest set
				mid[bit/8] |= 0x80 >> (bit % 8)
			}
		}
		l, _ := netip.AddrFromSlice(last)
		m, _ := netip.AddrFromSlice(mid)
		for _, a := range []netip.Addr{first, m, l} {
			out = append(out, a)
			if a.Is4() {
				out = append(out, netip.AddrFrom16(a.As16()))
			}
		}
	}
	return out
}
