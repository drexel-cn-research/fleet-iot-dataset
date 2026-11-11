package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"iotsim/internal/profile"
)

func cmdProfiles(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("profiles", flag.ExitOnError)
	extra := fs.String("profiles", "", "extra catalog JSON")
	api := fs.String("api", "", "the enrichment API base URL; if set, resolve each OUI and compare the vendor")
	checkDNS := fs.Bool("check-dns", false, "resolve every cloud, NTP and stream name in the catalog and list the ones that fail (run it on a Pi before a campaign)")
	fs.Parse(args)

	cat, err := profile.LoadCatalog(*extra)
	if err != nil {
		return err
	}
	if *checkDNS {
		return checkCatalogDNS(ctx, cat, net.DefaultResolver.LookupHost)
	}
	fmt.Printf("%-20s %-14s %-18s %-8s %s\n", "TYPE", "OUI", "VENDOR", "CLASS", "MODEL")
	for _, name := range cat.Names() {
		p := cat[name]
		line := fmt.Sprintf("%-20s %-14s %-18s %-8s %s", name, p.OUI, p.Vendor, p.Class, p.Model)
		if *api != "" {
			line += "   " + checkOUI(ctx, *api, p.OUI)
		}
		fmt.Println(line)
	}
	return nil
}

// checkOUI queries the enrichment API's /v1/oui/:mac with a MAC built from the OUI and
// returns what IEEE vendor it resolves to, so we can confirm each profile's
// OUI really belongs to the vendor we claim.
func checkOUI(ctx context.Context, base, oui string) string {
	mac := fmt.Sprintf("%s:%s:%s:00:00:00", oui[0:2], oui[2:4], oui[4:6])
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/v1/oui/"+mac, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "api: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Sprintf("api: %s", resp.Status)
	}
	var out struct {
		Match *struct {
			Org    string `json:"org"`
			Vendor string `json:"vendor"`
		} `json:"match"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Match == nil {
		return "-> (no IEEE match)"
	}
	return "-> " + out.Match.Org
}

// profileHosts lists the DNS names a profile contacts: cloud endpoints, its
// NTP server and its stream domains. Templated names ({serial}, ...) differ
// per device and are skipped.
func profileHosts(p *profile.Profile) []string {
	set := map[string]bool{}
	add := func(h string) {
		h = strings.TrimSpace(h)
		if h != "" && !strings.Contains(h, "{") && net.ParseIP(h) == nil {
			set[strings.ToLower(h)] = true
		}
	}
	for _, e := range p.Cloud {
		add(e.Host)
	}
	if p.NTP != nil {
		add(p.NTP.Server)
	}
	if p.Stream != nil {
		for _, h := range p.Stream.Domains {
			add(h)
		}
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// checkCatalogDNS resolves every name in the catalog and reports the ones
// that fail. Vendors retire names: three catalog names stopped resolving in
// the first week, and a device whose names are dead produces no cloud
// traffic, which quietly skews the data.
func checkCatalogDNS(ctx context.Context, cat profile.Catalog, lookup func(context.Context, string) ([]string, error)) error {
	users := map[string][]string{}
	for _, name := range cat.Names() {
		for _, h := range profileHosts(cat[name]) {
			users[h] = append(users[h], name)
		}
	}
	hosts := make([]string, 0, len(users))
	for h := range users {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	dead := 0
	for _, h := range hosts {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := lookup(c, h)
		cancel()
		if err != nil {
			dead++
			fmt.Printf("FAIL  %-45s %s   (%s)\n", h, strings.Join(users[h], ", "), err)
		}
	}
	fmt.Printf("%d of %d names resolve\n", len(hosts)-dead, len(hosts))
	if dead > 0 {
		return fmt.Errorf("%d catalog name(s) do not resolve", dead)
	}
	return nil
}
