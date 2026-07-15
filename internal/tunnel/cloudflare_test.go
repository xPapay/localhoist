package tunnel

import "testing"

func TestParseCloudflaredLine(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		wantURL string
		wantErr string
	}{
		{
			name:    "banner URL line",
			line:    `2026-07-15T10:00:01Z INF |  https://real-words-here.trycloudflare.com                                                 |`,
			wantURL: "https://real-words-here.trycloudflare.com",
		},
		{
			name: "banner header line without URL",
			line: `2026-07-15T10:00:01Z INF |  Your quick Tunnel has been created! Visit it at (it may take some time to be reachable):  |`,
		},
		{
			name:    "error line",
			line:    `2026-07-15T10:00:02Z ERR Couldn't start tunnel error="request quick Tunnel: 429 Too Many Requests"`,
			wantErr: `Couldn't start tunnel error="request quick Tunnel: 429 Too Many Requests"`,
		},
		{
			name: "info noise ignored",
			line: `2026-07-15T10:00:00Z INF Version 2026.7.0 (Checksum abcdef)`,
		},
		{
			name: "terms-of-service line has no tunnel URL",
			line: `2026-07-15T10:00:00Z INF Thank you for trying Cloudflare Tunnel. https://www.cloudflare.com/website-terms/`,
		},
		{
			name: "plain non-log noise ignored",
			line: `some stray output`,
		},
	}
	for _, tc := range cases {
		url, errMsg := ParseCloudflaredLine([]byte(tc.line))
		if url != tc.wantURL || errMsg != tc.wantErr {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", tc.name, url, errMsg, tc.wantURL, tc.wantErr)
		}
	}
}
