package main

import (
	"context"
	"fmt"
	"io"
)

// runLogin prints the Spotify authorization URL on stdout. Unlike the wizard it
// keeps the terminal usable, so the URL can be copied or redirected to a
// clipboard tool without wrapping artifacts.
func runLogin(clientID string, stdout, stderr io.Writer) int {
	id, ok := normalizeClientID(clientID)
	if !ok {
		fmt.Fprintln(stderr, "No Spotify Client ID. Create one at https://developer.spotify.com/dashboard,")
		fmt.Fprintln(stderr, "then set PULSO_SPOTIFY_CLIENT_ID or add it in the Pulso setup wizard.")
		return 1
	}
	_, err := authorizeSpotify(context.Background(), id, func(raw string) {
		fmt.Fprintf(stdout, "Open this URL in your browser:\n%s\n", raw)
		_ = openBrowser(raw)
	})
	if err != nil {
		fmt.Fprintf(stderr, "Spotify authorization failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "Spotify authorization complete.")
	return 0
}
