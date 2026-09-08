package main

import (
	"encoding/hex"
	"net/netip"
	"testing"
)

func TestMarshalGeoIPList(t *testing.T) {
	t.Parallel()
	const (
		// GeoIP.country_code = "RU"
		countryCode = "0a025255"
		// GeoIP.cidr = CIDR{ip: 1.2.3.0, prefix: 24}
		firstCIDR = "12080a04010203001018"
		// GeoIP.cidr = CIDR{ip: 2001:db8::, prefix: 32}
		secondCIDR = "12140a1020010db8000000000000000000000000" + "1020"
		// GeoIPList.entry, holding the 36-byte GeoIP above
		entry = "0a24" + countryCode + firstCIDR + secondCIDR
	)
	list := marshalGeoIPList([]GeoIPEntry{{
		Code: "ru",
		Prefixes: []netip.Prefix{
			netip.MustParsePrefix("1.2.3.0/24"),
			netip.MustParsePrefix("2001:db8::/32"),
		},
	}})
	if got := hex.EncodeToString(list); got != entry {
		t.Fatalf("marshalGeoIPList\n got %s\nwant %s", got, entry)
	}
}

func TestMarshalCIDRUnmapsIPv4(t *testing.T) {
	t.Parallel()
	mapped := hex.EncodeToString(marshalCIDR(netip.MustParsePrefix("::ffff:1.2.3.0/120")))
	plain := hex.EncodeToString(marshalCIDR(netip.MustParsePrefix("1.2.3.0/24")))
	if mapped != plain {
		t.Fatalf("mapped %s, plain %s", mapped, plain)
	}
}

func TestParsePrefix(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		input string
		want  string
	}{
		{"192.0.2.1", "192.0.2.1/32"},
		{"192.0.2.5/24", "192.0.2.0/24"},
		{"::ffff:192.0.2.0/120", "192.0.2.0/24"},
		{"2001:db8::1", "2001:db8::1/128"},
	} {
		prefix, err := parsePrefix(testCase.input)
		if err != nil {
			t.Fatalf("parsePrefix(%q): %v", testCase.input, err)
		}
		if prefix.String() != testCase.want {
			t.Fatalf("parsePrefix(%q) = %s, want %s", testCase.input, prefix, testCase.want)
		}
	}
}
