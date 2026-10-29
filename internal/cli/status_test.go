package cli

import (
	"testing"
	"time"

	"flock/internal/api"
	"flock/internal/share"
)

func TestShareLineNamesEveryState(t *testing.T) {
	ends := time.Date(2026, 10, 19, 18, 42, 0, 0, time.Local)
	for _, tc := range []struct {
		st   api.ShareStatus
		want string
	}{
		{api.ShareStatus{Site: "acme", State: share.Building}, "acme: building the assets"},
		{api.ShareStatus{Site: "acme", State: share.Starting}, "acme: starting the tunnel"},
		{api.ShareStatus{Site: "acme", State: share.Failed, Error: "cloudflared exited"}, "acme: failed: cloudflared exited"},
		{api.ShareStatus{Site: "acme", State: share.Running, URL: "https://a-b.trycloudflare.com", ExpiresAt: &ends},
			"acme → https://a-b.trycloudflare.com (ends 6:42PM)"},
		{api.ShareStatus{Site: "acme", State: share.Running, URL: "https://a-b.trycloudflare.com", ExpiresAt: &ends, Allow: []string{"bob@acme.com", "*@client.com"}},
			"acme → https://a-b.trycloudflare.com (ends 6:42PM, only bob@acme.com, *@client.com)"},
	} {
		if got := shareLine(tc.st); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}
