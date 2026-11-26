package spotify

import (
	"errors"
	"net/url"
	"os/exec"
	"runtime"
)

// OpenBrowser hands the authorization URL to the default browser. It is a
// convenience: the URL is also shown in the terminal and can be pasted.
func OpenBrowser(raw string) error {
	link, err := url.Parse(raw)
	if err != nil || (link.Scheme != "https" && link.Scheme != "http") || link.Host == "" {
		return errors.New("refusing to open a non-web URL")
	}
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", raw).Start()
	case "darwin":
		return exec.Command("open", raw).Start()
	default:
		return exec.Command("xdg-open", raw).Start()
	}
}
