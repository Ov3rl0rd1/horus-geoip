package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"strings"
)

// Country fields of the MaxMind database that a network's code can be taken
// from. "country" is where the network is geolocated, "registered" is the
// country of the organisation the range is registered to, "both" indexes the
// network under either code.
const (
	fieldCountry    = "country"
	fieldRegistered = "registered"
	fieldBoth       = "both"
)

// Config is the build configuration, read from config.json.
type Config struct {
	// SourceRepository is the owner/name of the GitHub repository whose latest
	// release carries the MaxMind database.
	SourceRepository string `json:"source_repository"`
	// SourceAsset is the release asset to download.
	SourceAsset string `json:"source_asset"`
	// CountryField selects which country code of a network is used, one of
	// "country", "registered" or "both".
	CountryField string `json:"country_field"`
	// CountryCodes lists the ISO country codes to collect, lowercase.
	CountryCodes []string `json:"country_codes"`
	// OutputFile is the geoip.dat file to write.
	OutputFile string `json:"output_file"`
	// Category is the code the collected networks are written under, queried
	// from xray-core as "geoip:<category>".
	Category string `json:"category"`
	// ExcludeCategory, when set, writes Exclude to the same file as a second
	// category so it can be matched by a routing rule of its own. Networks in
	// Exclude are always removed from Category regardless of this setting.
	ExcludeCategory string `json:"exclude_category"`
	// Include lists extra addresses or CIDRs to add to Category.
	Include []string `json:"include"`
	// Exclude lists addresses or CIDRs to subtract from Category. Ranges are
	// split as needed, so excluding a network inside a larger one works.
	Exclude []string `json:"exclude"`

	include []netip.Prefix
	exclude []netip.Prefix
}

func loadConfig(path string) (*Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err = config.parse(); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &config, nil
}

func (c *Config) parse() error {
	if c.SourceRepository == "" {
		return fmt.Errorf("missing source_repository")
	}
	if c.SourceAsset == "" {
		return fmt.Errorf("missing source_asset")
	}
	if c.OutputFile == "" {
		return fmt.Errorf("missing output_file")
	}
	if c.Category == "" {
		return fmt.Errorf("missing category")
	}
	if len(c.CountryCodes) == 0 {
		return fmt.Errorf("missing country_codes")
	}
	switch c.CountryField {
	case "":
		c.CountryField = fieldBoth
	case fieldCountry, fieldRegistered, fieldBoth:
	default:
		return fmt.Errorf("invalid country_field %q, expected %q, %q or %q", c.CountryField, fieldCountry, fieldRegistered, fieldBoth)
	}
	for index, code := range c.CountryCodes {
		c.CountryCodes[index] = strings.ToLower(code)
	}
	var err error
	if c.include, err = parsePrefixes(c.Include); err != nil {
		return fmt.Errorf("include: %w", err)
	}
	if c.exclude, err = parsePrefixes(c.Exclude); err != nil {
		return fmt.Errorf("exclude: %w", err)
	}
	return nil
}

// parsePrefixes accepts both CIDRs ("192.0.2.0/24") and bare addresses
// ("192.0.2.1"), the latter as a single-address prefix.
func parsePrefixes(list []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(list))
	for _, item := range list {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		prefix, err := parsePrefix(item)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func parsePrefix(item string) (netip.Prefix, error) {
	if strings.Contains(item, "/") {
		prefix, err := netip.ParsePrefix(item)
		if err != nil {
			return netip.Prefix{}, err
		}
		return unmap(prefix.Masked()), nil
	}
	addr, err := netip.ParseAddr(item)
	if err != nil {
		return netip.Prefix{}, err
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// unmap rewrites an IPv4-mapped IPv6 prefix such as ::ffff:192.0.2.0/120 to its
// plain IPv4 form, so that a range is only ever represented one way.
func unmap(prefix netip.Prefix) netip.Prefix {
	addr := prefix.Addr()
	if !addr.Is4In6() {
		return prefix
	}
	return netip.PrefixFrom(addr.Unmap(), prefix.Bits()-96)
}
