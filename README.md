# Pulso

Pulso is a Go terminal app in development for using Spotify with the official [Soloist](https://developer.spotify.com/documentation/soloist) playback engine. See [the plan](PLAN.md) for the Windows and Linux MVP.

## Run the setup wizard

From a Windows or Linux x86-64 terminal with Go 1.22 or newer, run `go run .`, or install a development checkout with `go install .` and run `pulso` with no arguments. The versioned install path will be available once the project has a published module path. The full-screen wizard shows a setup checklist and collapses to one pane in smaller terminals.

1. Pulso checks for Soloist and reuses a running session when its local WebSocket is available. If Soloist is missing, press **Y** to download and install the [official Spotify build](https://developer.spotify.com/documentation/soloist/reference/downloads-and-updates). Nothing downloads unless you confirm.
2. If Pulso needs to start a stopped daemon, open the [Soloist dashboard](https://developer.spotify.com/dashboard/soloist), sign in with Spotify Premium, and create a Soloist key. Paste it into the masked prompt and press **Enter**. Pulso does not save the key. Spotify requires it in the daemon's launch arguments, so local process inspection may reveal it. Pulso asks for the key again whenever it needs to launch a stopped daemon.
3. If you're already paired, Pulso opens **Now Playing** immediately. Otherwise, leave Pulso open and connect your phone and computer to the same local network. Open Spotify on your phone with the same account, play a song, tap **Connect to a device**, and select the device name shown by Pulso. If no name is available, select **Pulso** or your existing Soloist device name. Pulso opens **Now Playing** automatically when Spotify connects. If the device is missing, check that guest Wi-Fi or a VPN isn't blocking local discovery.

While waiting for pairing, Pulso retries a lost connection with a short delay between attempts. Press **Enter** to check immediately or **Esc** to quit. If Soloist has stopped, start it again and press **Enter**; the wizard will not restart it during pairing.

Pulso searches `PATH` first, then `pulso/bin` under your user configuration directory (`%APPDATA%` on Windows; `$XDG_CONFIG_HOME` or `~/.config` on Linux). Installing via Pulso does not require admin access or change `PATH`; the install directory's filesystem must support hard links. The wizard only uses Soloist's default session directory: `%LOCALAPPDATA%/soloist/data` on Windows, or `$STATE_DIRECTORY` if set on Linux, then `$XDG_DATA_HOME/soloist`, then `$HOME/.local/share/soloist`. It only connects to numeric loopback WebSocket addresses; custom data directories and non-loopback endpoints aren't supported by the wizard.

Setup is intended for the official Windows and Linux x86-64 Soloist builds. Pulso does not install system audio dependencies, configure audio devices, or update Soloist automatically. Spotify Connect pairing and playback were confirmed by a user on Windows; native Linux, the Web API flow, and versioned installation still need verification. When Soloist is ready, Pulso opens the **Now Playing** workspace with search on top, the library on the left, the queue or playlist in the middle, and a live playing bar above the key hints. Press **Space** to play or pause, **n/p** for next or previous, **+/-** for volume, **←/→** to seek, **s** to cycle shuffle, **r** to cycle repeat, **/** to search, **l** or **Tab** for the library, and **Esc** or **q** to exit. Below 100 columns or 30 rows the sidebar collapses to a one-line library switcher; below 60 columns the table keeps the number, title and duration, and the playing bar keeps the title and volume.

Pulso pauses playback when you quit. Soloist is a separate daemon: it keeps running with its pairing so the next launch is instant. To stop the daemon itself, end the `soloist` process or use `soloist ctl`.

## Search and playlists

Press **/** to focus the search field and **Enter** to search Spotify tracks; the results replace the table. Press **l** (or **Tab** to the sidebar) to browse your playlists, **Enter** to open one, **p** to play a playlist by its Spotify URI, and **Enter** on a track to play it. **Esc** leaves the search field or the results before it quits, and **Tab**/Shift+**Tab** move between the sidebar, the table, the search field and the playing bar. The hints at the bottom of the workspace follow the focused panel.

The same workspace shows one localized row for loading, empty, error, unauthorized and restricted states, in English or Spanish. Press **F2** for the language and **F3** for the theme at any size.

Searching uses the official Spotify Web API, which is a separate sign-in from Soloist:

1. Create a free app in the [Spotify developer dashboard](https://developer.spotify.com/dashboard) to get a Client ID. The app owner needs Spotify Premium, and Spotify allows up to five signed-in users.
2. Add `http://127.0.0.1:8888/callback` as a redirect URI for that app. Spotify requires the port in the URI, and it must match exactly.
3. The setup wizard asks for the Client ID once, right after the Soloist key, and shows the authorization URL. Pulso remembers only the public Client ID in `client-id` under your user configuration directory; the access token is never stored. The wizard always shows these instructions first, with your saved Client ID prefilled and editable. Press **s** in that step to continue without search and playlists. The fixed port keeps the redirect URI identical to the one Spotify accepts. You can also set `PULSO_SPOTIFY_CLIENT_ID` (PowerShell: `$env:PULSO_SPOTIFY_CLIENT_ID="<client-id>"`) to skip the prompt.
4. Approve access in the browser Pulso opens. Pulso waits on http://127.0.0.1:8888/callback and signs you in again on the next start; if that port is busy, close whatever uses it and retry.

Playlist contents are only returned for playlists you own or collaborate on. For other playlists, Pulso says the contents are restricted and can still play the playlist by its Spotify URI. Playback never depends on this sign-in, so the player keeps working when the Web API is unavailable.

## Language and theme

Press **F2** to switch between English and Spanish, and **F3** to cycle the **standard**, **dark**, and **light** themes. Pulso saves the choice in `preferences.json` under your user configuration directory and only writes it when you press one of those keys.

## Diagnose Soloist

If the automatic install fails, the wizard offers recovery steps. You can use the official Spotify download, add its executable directory to `PATH`, and reopen Pulso from a terminal with the updated `PATH`.

Run `pulso doctor` (or `go run . doctor` from the checkout) if the wizard can't check or start Soloist. The diagnostic reports Soloist's own daemon, WebSocket, and sign-in status without installing or starting it. Use `pulso doctor --data-dir PATH` for an existing daemon with a custom directory.

If runtime files are stale or the WebSocket is broken, Pulso leaves them and any existing daemon alone rather than starting a duplicate. Check the diagnostic and restore the daemon's loopback WebSocket or stop the existing instance with Soloist's controls before retrying. Don't delete runtime files while a daemon may be using them.
