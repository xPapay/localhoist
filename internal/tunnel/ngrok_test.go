package tunnel

import "testing"

func TestParseLogLine(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		wantURL string
		wantErr string
	}{
		{
			name:    "started tunnel",
			line:    `{"addr":"http://127.0.0.1:54321","lvl":"info","msg":"started tunnel","name":"command_line","obj":"tunnels","url":"https://abc123.ngrok-free.app"}`,
			wantURL: "https://abc123.ngrok-free.app",
		},
		{
			name:    "error line",
			line:    `{"err":"authentication failed: Usage of ngrok requires a verified account","lvl":"eror","msg":"session closed"}`,
			wantErr: "authentication failed: Usage of ngrok requires a verified account",
		},
		{
			name: "info noise ignored",
			line: `{"lvl":"info","msg":"client session established"}`,
		},
		{
			name: "non-json ignored",
			line: `plain text line`,
		},
		{
			name:    "crit with nil err falls back to msg",
			line:    `{"err":"<nil>","lvl":"crit","msg":"failed to reconnect session"}`,
			wantErr: "failed to reconnect session",
		},
	}
	for _, tc := range cases {
		url, errMsg := ParseLogLine([]byte(tc.line))
		if url != tc.wantURL || errMsg != tc.wantErr {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", tc.name, url, errMsg, tc.wantURL, tc.wantErr)
		}
	}
}
