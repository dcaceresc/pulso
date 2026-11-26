package spotify

import "testing"

func TestOpenBrowserRejectsNonWebURLs(t *testing.T) {
	for _, raw := range []string{"", "file:///etc/passwd", "cmd:calc", "javascript:alert(1)", "https:///nohost"} {
		if err := OpenBrowser(raw); err == nil {
			t.Fatalf("opened %q, want refusal", raw)
		}
	}
}
