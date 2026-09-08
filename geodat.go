package main

// Minimal encoder for the GeoIP database format read by xray-core.
//
// A geoip.dat file is a serialised GeoIPList message, defined in xray-core's
// app/router/config.proto (and, wire-identical, in v2fly's
// app/router/routercommon/common.proto):
//
//	message CIDR      { bytes ip = 1; uint32 prefix = 2; }
//	message GeoIP     { string country_code = 1; repeated CIDR cidr = 2; }
//	message GeoIPList { repeated GeoIP entry = 1; }
//
// Only those fields are written; the remaining ones are load-time metadata that
// xray-core fills in itself. Encoding the two messages by hand keeps this
// repository free of the v2ray/xray module trees, which exist only to provide
// the generated types.

import (
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

const (
	wireVarint = 0
	wireBytes  = 2
)

// GeoIPEntry is one category of a geoip.dat file, matched from a routing rule
// as "geoip:<code>".
type GeoIPEntry struct {
	Code     string
	Prefixes []netip.Prefix
}

func appendVarint(buffer []byte, value uint64) []byte {
	for value >= 0x80 {
		buffer = append(buffer, byte(value)|0x80)
		value >>= 7
	}
	return append(buffer, byte(value))
}

func appendTag(buffer []byte, field int, wireType int) []byte {
	return appendVarint(buffer, uint64(field)<<3|uint64(wireType))
}

func appendVarintField(buffer []byte, field int, value uint64) []byte {
	return appendVarint(appendTag(buffer, field, wireVarint), value)
}

func appendBytesField(buffer []byte, field int, value []byte) []byte {
	buffer = appendVarint(appendTag(buffer, field, wireBytes), uint64(len(value)))
	return append(buffer, value...)
}

func marshalCIDR(prefix netip.Prefix) []byte {
	prefix = unmap(prefix)
	buffer := appendBytesField(nil, 1, prefix.Addr().AsSlice())
	return appendVarintField(buffer, 2, uint64(prefix.Bits()))
}

func marshalGeoIP(entry GeoIPEntry) []byte {
	// xray-core upper-cases the code it looks for, so the stored code has to be
	// upper case as well. This matches the official geoip.dat files.
	buffer := appendBytesField(nil, 1, []byte(strings.ToUpper(entry.Code)))
	for _, prefix := range entry.Prefixes {
		buffer = appendBytesField(buffer, 2, marshalCIDR(prefix))
	}
	return buffer
}

func marshalGeoIPList(entries []GeoIPEntry) []byte {
	var buffer []byte
	for _, entry := range entries {
		buffer = appendBytesField(buffer, 1, marshalGeoIP(entry))
	}
	return buffer
}

func writeGeoIP(path string, entries []GeoIPEntry) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	log.Println("write", absolute)
	return os.WriteFile(path, marshalGeoIPList(entries), 0o644)
}
