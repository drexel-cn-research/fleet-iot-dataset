package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"iotsim/internal/profile"
)

func TestCheckCatalogDNS(t *testing.T) {
	cat, err := profile.LoadCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	lookup := func(_ context.Context, h string) ([]string, error) {
		asked = append(asked, h)
		if h == "frontdoor.nest.com" {
			return nil, errors.New("no such host")
		}
		return []string{"192.0.2.1"}, nil
	}
	err = checkCatalogDNS(context.Background(), cat, lookup)
	if err == nil || !strings.Contains(err.Error(), "1 catalog name") {
		t.Fatalf("err = %v, want one dead name", err)
	}
	for _, h := range asked {
		if strings.Contains(h, "{") {
			t.Fatalf("templated name %q was looked up", h)
		}
	}
	if len(asked) < 20 {
		t.Fatalf("only %d names checked; expected the whole catalog", len(asked))
	}
	// The retired Nest name is gone from the catalog.
	for _, h := range asked {
		if h == "transport.home.nest.com" {
			t.Fatal("transport.home.nest.com is still in the catalog")
		}
	}
}
