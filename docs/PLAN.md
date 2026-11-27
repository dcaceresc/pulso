# Pulso — Spotify in the terminal

Pulso is a Go terminal UI for searching Spotify, browsing playlists, and playing music without keeping the official Spotify app open after initial pairing. It uses Spotify Soloist for audio, not `librespot`. The MVP targets Windows x86-64 and Linux x86-64.

## Install and run

Once the repository is published, the root Go package will expose the `pulso` command:

```sh
go install github.com/YOUR_USERNAME/pulso@latest
pulso
```

The module path is a placeholder until the repository owner is known. `go install` builds and installs Pulso; it does not run a setup hook. Pulso checks Soloist on first launch and whenever it needs to connect.

| Soloist state | Pulso behavior |
| --- | --- |
| Running, reachable, and paired | Reuse it and open the player. |
| Installed but stopped | Start it with local WebSocket control and the user's existing configuration. |
| Missing | Offer to download the matching [official Spotify build][downloads] directly and install it in a per-user directory, without admin permissions or `PATH` changes. |
| Running without a reachable WebSocket | Explain how to enable it or configure its endpoint. Do not start a duplicate process. |
| Not paired | Guide the user to generate their own Soloist key with a Premium account and pair once through the official Spotify app. Preserve the session for later launches. |
| Build expired (daemon exit code `10`) | Offer to update a Pulso-managed copy without losing pairing. For an external copy, show update instructions rather than replacing it. |
| Unsupported system or architecture | Explain the limitation and skip the download. Compiling Pulso does not imply Soloist support. |

Check `PATH` and the managed install directory for the executable, and treat executable presence, daemon reachability, and pairing as separate checks. Bind a Pulso-started daemon's WebSocket to loopback. Do not bundle or redistribute Soloist, log keys, or replace a working managed binary until its replacement has been staged and checked. Keep the executable separate from session data.

## Architecture

| Component | Responsibility |
| --- | --- |
| Go + Bubble Tea | Keyboard-driven views for Now Playing, search, playlists, queue, and first-run setup. |
| [Soloist local WebSocket][WebSocket API] | Play by Spotify URI, control playback, and receive state and queue events. Reconnect after a daemon restart. |
| Spotify Web API | Search and list the user's playlists. Use OAuth authorization code with [PKCE], a loopback redirect, and no embedded client secret. If the browser cannot be opened automatically, print the authorization URL. |

Keep playback independent of Web API authentication and quotas: controls, the queue, and playing known links must still work when search and library are unavailable. Under Spotify's [development mode] rules, playlist contents are only available for playlists the user owns or collaborates on. If contents are unavailable, indicate the limitation and offer playback of the playlist by URI rather than showing it as empty.

## Milestones

1. **Validate the integrations on both platforms.** Pair Soloist, close the official app, and confirm that a track starts by URI and plays locally. Independently confirm that the developer app can search and list playlists for the target account.
2. **Build first-run setup.** Detect, download with confirmation, start, reuse, and update Soloist. Exercise missing, stopped, unpaired, unreachable, and expired states.
3. **Build the player.** Show track, artist, progress, queue, and connection state. Add keyboard controls for pause, skip, volume, shuffle, and repeat, plus playback by pasted link.
4. **Add search and playlists.** Search by name with pagination, play a result, add a track to the queue, and start playback from a listed playlist.
5. **Verify the release flow.** Test both API clients with local fake servers and test the installer with local archive fixtures. Verify `go install` from a versioned module and run a real end-to-end check on Windows and Linux.

## MVP acceptance

- [ ] `go install` installs `pulso` on Windows and Linux x86-64.
- [ ] On each platform, first launch can offer to install the official Soloist build when missing, and a later launch can reuse an already working installation.
- [ ] After initial pairing, the user can open Pulso, search for a track, play it, queue another, and switch to a playlist without keeping the official app open.
- [ ] With the Web API authorized, the user can add the playing track to a chosen playlist and remove one occurrence of a track from the playlist being browsed, both after an explicit confirmation.
- [ ] Known links and local playback controls still work if Spotify Web API access is unavailable.

Out of scope for the MVP: lyrics and validating additional platforms or architectures. Playlist edits are limited to adding the playing track and removing one occurrence of a chosen track; creating playlists, reordering or replacing their items stays out of scope.

[downloads]: https://developer.spotify.com/documentation/soloist/reference/downloads-and-updates
[WebSocket API]: https://developer.spotify.com/documentation/soloist/reference/websocket-api
[PKCE]: https://developer.spotify.com/documentation/web-api/tutorials/code-pkce-flow
[Development mode]: https://developer.spotify.com/documentation/web-api/tutorials/february-2026-migration-guide
