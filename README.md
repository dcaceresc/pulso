# Pulso

Pulso is a Go terminal app in development for using Spotify with the official [Soloist](https://developer.spotify.com/documentation/soloist) playback engine. See [the plan](PLAN.md) for the Windows and Linux MVP.

## Run the setup wizard

From a Windows or Linux x86-64 terminal with Go 1.22 or newer, run `go run .`, or install a development checkout with `go install .` and run `pulso` with no arguments. The versioned install path will be available once the project has a published module path. The full-screen wizard shows a setup checklist and collapses to one pane in smaller terminals.

1. Pulso checks for Soloist and reuses a running session when its local WebSocket is available. If Soloist is missing, press **Y** to download and install the [official Spotify build](https://developer.spotify.com/documentation/soloist/reference/downloads-and-updates). Nothing downloads unless you confirm.
2. If Pulso needs to start a stopped daemon, open the [Soloist dashboard](https://developer.spotify.com/dashboard/soloist), sign in with Spotify Premium, and create a Soloist key. Paste it into the masked prompt and press **Enter**. Pulso does not save the key. Spotify requires it in the daemon's launch arguments, so local process inspection may reveal it. Pulso asks for the key again whenever it needs to launch a stopped daemon.
3. If you're already paired, Pulso opens **Now Playing** immediately. Otherwise, leave Pulso open and connect your phone and computer to the same local network. Open Spotify on your phone with the same account, play a song, tap **Connect to a device**, and select the device name shown by Pulso. If no name is available, select **Pulso** or your existing Soloist device name. Pulso opens **Now Playing** automatically when Spotify connects. If the device is missing, check that guest Wi-Fi or a VPN isn't blocking local discovery.

While waiting for pairing, Pulso retries a lost connection with a short delay between attempts. Press **Enter** to check immediately or **Esc** to quit. If Soloist has stopped, start it again and press **Enter**; the wizard will not restart it during pairing.

Pulso searches `PATH` first, then `pulso/bin` under your user configuration directory (`%APPDATA%` on Windows; `$XDG_CONFIG_HOME` or `~/.config` on Linux). Installing via Pulso does not require admin access or change `PATH`; the install directory's filesystem must support hard links. The wizard only uses Soloist's default session directory: `%LOCALAPPDATA%/soloist/data` on Windows, or `$STATE_DIRECTORY` if set on Linux, then `$XDG_DATA_HOME/soloist`, then `$HOME/.local/share/soloist`. It only connects to numeric loopback WebSocket addresses; custom data directories and non-loopback endpoints aren't supported by the wizard.

Setup is intended for the official Windows and Linux x86-64 Soloist builds. Pulso does not install system audio dependencies, configure audio devices, or update Soloist automatically. Real playback, account pairing, and versioned installation on both targets have not yet been verified. When Soloist is ready, Pulso opens **Now Playing** with the current track, progress, volume, and upcoming queue. Press **Space** to play or pause, **n/p** for next or previous, **+/-** for volume, and **Esc** or **q** to exit. Search and playlist browsing via Spotify Web API are not implemented yet.

## Diagnose Soloist

If the automatic install fails, the wizard offers recovery steps. You can use the official Spotify download, add its executable directory to `PATH`, and reopen Pulso from a terminal with the updated `PATH`.

Run `pulso doctor` (or `go run . doctor` from the checkout) if the wizard can't check or start Soloist. The diagnostic reports Soloist's own daemon, WebSocket, and sign-in status without installing or starting it. Use `pulso doctor --data-dir PATH` for an existing daemon with a custom directory.

If runtime files are stale or the WebSocket is broken, Pulso leaves them and any existing daemon alone rather than starting a duplicate. Check the diagnostic and restore the daemon's loopback WebSocket or stop the existing instance with Soloist's controls before retrying. Don't delete runtime files while a daemon may be using them.
