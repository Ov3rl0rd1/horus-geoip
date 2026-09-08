package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strings"

	"github.com/google/go-github/v45/github"
	"github.com/oschwald/geoip2-golang"
	"github.com/oschwald/maxminddb-golang"
	"go4.org/netipx"
)

var githubClient *github.Client

func init() {
	log.SetFlags(0)
	accessToken, loaded := os.LookupEnv("ACCESS_TOKEN")
	if !loaded {
		githubClient = github.NewClient(nil)
		return
	}
	transport := &github.BasicAuthTransport{
		Username: accessToken,
	}
	githubClient = github.NewClient(transport.Client())
}

// fetch returns the release the database is taken from: the one named by
// FIXED_RELEASE when that is set, the latest one otherwise.
func fetch(from string) (*github.RepositoryRelease, error) {
	names := strings.SplitN(from, "/", 2)
	if len(names) != 2 {
		return nil, fmt.Errorf("invalid repository %q, expected owner/name", from)
	}
	if fixedRelease := os.Getenv("FIXED_RELEASE"); fixedRelease != "" {
		release, _, err := githubClient.Repositories.GetReleaseByTag(context.Background(), names[0], names[1], fixedRelease)
		return release, err
	}
	release, _, err := githubClient.Repositories.GetLatestRelease(context.Background(), names[0], names[1])
	return release, err
}

func get(downloadURL string) ([]byte, error) {
	log.Println("download", downloadURL)
	response, err := http.Get(downloadURL)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", downloadURL, response.Status)
	}
	return io.ReadAll(response.Body)
}

func download(release *github.RepositoryRelease, assetName string) ([]byte, error) {
	for _, asset := range release.Assets {
		if asset.GetName() == assetName {
			return get(asset.GetBrowserDownloadURL())
		}
	}
	return nil, fmt.Errorf("%s not found in upstream release %s", assetName, release.GetTagName())
}

// parse groups every network of the MaxMind database by lowercase country code.
func parse(binary []byte, countryField string) (map[string][]netip.Prefix, error) {
	database, err := maxminddb.FromBytes(binary)
	if err != nil {
		return nil, err
	}
	countryMap := make(map[string][]netip.Prefix)
	networks := database.Networks(maxminddb.SkipAliasedNetworks)
	var country geoip2.Country
	for networks.Next() {
		ipNet, err := networks.Network(&country)
		if err != nil {
			return nil, err
		}
		prefix, ok := toPrefix(ipNet)
		if !ok {
			continue
		}
		for _, code := range countryCodes(&country, countryField) {
			countryMap[code] = append(countryMap[code], prefix)
		}
	}
	if err = networks.Err(); err != nil {
		return nil, err
	}
	return countryMap, nil
}

func countryCodes(country *geoip2.Country, countryField string) []string {
	var codes []string
	if countryField != fieldRegistered {
		if code := strings.ToLower(country.Country.IsoCode); code != "" {
			codes = append(codes, code)
		}
	}
	if countryField != fieldCountry {
		code := strings.ToLower(country.RegisteredCountry.IsoCode)
		if code != "" && !slices.Contains(codes, code) {
			codes = append(codes, code)
		}
	}
	return codes
}

// toPrefix converts a network of the MaxMind reader, which reports IPv4 either
// as a 4-byte address or mapped into the IPv6 space, to a plain netip.Prefix.
func toPrefix(ipNet *net.IPNet) (netip.Prefix, bool) {
	addr, ok := netip.AddrFromSlice(ipNet.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	ones, bits := ipNet.Mask.Size()
	if bits == 0 {
		return netip.Prefix{}, false
	}
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	if addr.Is4() && bits == 128 {
		ones -= 96
	}
	if ones < 0 || ones > addr.BitLen() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(addr, ones), true
}

func mergePrefixes(prefixes []netip.Prefix) ([]netip.Prefix, error) {
	var builder netipx.IPSetBuilder
	for _, prefix := range prefixes {
		builder.AddPrefix(prefix)
	}
	set, err := builder.IPSet()
	if err != nil {
		return nil, err
	}
	return set.Prefixes(), nil
}

func generate(config *Config) (string, error) {
	release, err := fetch(config.SourceRepository)
	if err != nil {
		return "", err
	}
	binary, err := download(release, config.SourceAsset)
	if err != nil {
		return "", err
	}
	countryMap, err := parse(binary, config.CountryField)
	if err != nil {
		return "", err
	}
	var builder netipx.IPSetBuilder
	for _, code := range config.CountryCodes {
		prefixes := countryMap[code]
		if len(prefixes) == 0 {
			return "", fmt.Errorf("country code %q not found in %s", code, config.SourceAsset)
		}
		log.Printf("country %s: %d networks", code, len(prefixes))
		for _, prefix := range prefixes {
			builder.AddPrefix(prefix)
		}
	}
	for _, prefix := range config.include {
		builder.AddPrefix(prefix)
	}
	for _, prefix := range config.exclude {
		builder.RemovePrefix(prefix)
	}
	set, err := builder.IPSet()
	if err != nil {
		return "", err
	}
	entries := []GeoIPEntry{{Code: config.Category, Prefixes: set.Prefixes()}}
	if config.ExcludeCategory != "" && len(config.exclude) > 0 {
		excluded, err := mergePrefixes(config.exclude)
		if err != nil {
			return "", err
		}
		entries = append(entries, GeoIPEntry{Code: config.ExcludeCategory, Prefixes: excluded})
	}
	for _, entry := range entries {
		log.Printf("category %s: %d networks", strings.ToUpper(entry.Code), len(entry.Prefixes))
	}
	if err = writeGeoIP(config.OutputFile, entries); err != nil {
		return "", err
	}
	return release.GetTagName(), nil
}

// setActionOutput publishes a step output when running under GitHub Actions.
func setActionOutput(name string, content string) error {
	outputPath := os.Getenv("GITHUB_OUTPUT")
	if outputPath == "" {
		return nil
	}
	file, err := os.OpenFile(outputPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintf(file, "%s=%s\n", name, content)
	return err
}

func main() {
	configPath := os.Getenv("CONFIG")
	if configPath == "" {
		configPath = "config.json"
	}
	config, err := loadConfig(configPath)
	if err != nil {
		log.Fatal(err)
	}
	tag, err := generate(config)
	if err != nil {
		log.Fatal(err)
	}
	if err = setActionOutput("tag", tag); err != nil {
		log.Fatal(err)
	}
}
