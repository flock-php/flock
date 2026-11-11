package config

import (
	_ "embed"
	"strings"
)

// tlds.txt is the list of labels in the public DNS root zone, taken from IANA
// (https://data.iana.org/TLD/tlds-alpha-by-domain.txt). Refresh it with
// `go run ./tools/gen-tlds`; it is committed, so builds and tests never need the network.
//
//go:embed tlds.txt
var tldsList string

// publicTLDs is tlds.txt as a set: every top-level label that resolves on the public internet.
// Serving sites under one of these would shadow real internet names (dev and app are
// HSTS-preloaded on top of that), so ValidateTLD refuses them.
var publicTLDs map[string]struct{}

func init() {
	labels := strings.Fields(tldsList)
	publicTLDs = make(map[string]struct{}, len(labels))
	for _, l := range labels {
		publicTLDs[l] = struct{}{}
	}
}

// IsPublicTLD reports whether tld (normalized, lowercase) names a top-level domain of the
// public root zone. The set itself stays unexported; the predicate is exported so the CLI can
// point at the reason when it refuses a chosen TLD.
func IsPublicTLD(tld string) bool {
	_, ok := publicTLDs[tld]
	return ok
}
