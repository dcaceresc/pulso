# Pulso instructions

English · [Español (predeterminado)](INSTRUCTIONS.md)

This guide explains how to install, configure and use Pulso. The app targets my personal workflow: listening to Spotify from the terminal on Windows and Linux through Spotify's official player instead of `librespot`.

## Contents

- [Recommended install](#recommended-install)
- [First launch](#first-launch)
- [Pairing Soloist](#pairing-soloist)
- [Connecting Spotify for search and playlists](#connecting-spotify-for-search-and-playlists)
- [Daily use](#daily-use)
- [Shortcuts](#shortcuts)
- [Language and themes](#language-and-themes)
- [Where data lives](#where-data-lives)
- [Diagnostics and troubleshooting](#diagnostics-and-troubleshooting)
- [Tests](#tests)
- [Known limits](#known-limits)
- [Repository layout](#repository-layout)

## Recommended install

Install Pulso once from the repository root, then run it from any folder:

```sh
go install ./cmd/pulso
pulso
```

Go 1.22 or newer is only needed to build. The installed binary does not need Go. Audio comes from **Spotify Soloist**, which Pulso downloads into your user directory when missing; no administrator rights and no `PATH` changes.

A `go install` build does not update itself: rerun the command when the repository changes.

## First launch

Running `pulso` with no arguments opens the full-screen wizard. It checks, in order:

1. Whether Soloist is installed and answering on its local WebSocket.
2. If missing, it offers Spotify's official build for your system. Nothing downloads without your `Y`.
3. If Soloist is installed but stopped, it asks for the Soloist key to start it.
4. If a stored Spotify session is still valid, it goes straight to the player.

## Pairing Soloist

Spotify authorizes the pairing from its own app; it does not happen inside Pulso:

1. Create your Soloist key in the [Spotify developer dashboard](https://developer.spotify.com/dashboard/soloist). You need Spotify Premium and the key is personal: do not share or publish it.
2. Keep the wizard open and connect the phone and the computer to the same local network.
3. In the Spotify app on your phone, play something, open the device picker and select **Pulso** (or your existing Soloist device name).
4. As soon as Spotify confirms the connection, Pulso opens the player by itself. Enter still works to check immediately.

If the device does not appear, check for guest Wi-Fi, client isolation or a VPN blocking local discovery.

If pairing does not advance because the key was mistyped, press `b`: the wizard returns to the key step with an empty field. Submitting the new key makes Pulso stop the Soloist instance it had started with the previous key and start it again; `Enter` only rechecks the status.

### Pairing diagnostics

In the pairing step the wizard diagnoses why the phone may not see the computer and summarises the result with an indicator at the start of the line: `● OK` in green when there is nothing to review, `● Review` in amber when there is a risk but pairing can still work (a container bridge competing for the announcement, for example), and `● Blocked` in red when a rule is missing and the phone will not be able to connect. The word always accompanies the colour, so it still reads on a terminal without colour.

The `d: diagnostics` shortcut appears only when there is something to review: it opens the detail with the findings and, when there is something to apply, the exact commands. `Enter` returns to the pairing step. When the detail can propose no command, it says so and offers no `a`.

The diagnosis checks three things:

- The address your machine uses to reach the local network.
- The interfaces that compete for the single address Soloist announces: container bridges (Docker's, for example), VPNs and tunnels.
- The firewall state and the rules that are missing.

A bridge or a VPN breaks pairing because Soloist announces one address at startup and offers no option to pin the interface: it can advertise the bridge address instead of your LAN address, and the phone searches the wrong network. The concrete recovery is to start Pulso with that bridge absent, pair, and bring the bridge back afterwards. The advertisement stays valid until Soloist restarts.

Soloist's control port is chosen at startup and changes, so a rule for one port goes stale on the next start. What is recommended is a LAN-scoped allow (for example `sudo ufw allow from 192.168.1.0/24`), which keeps working after the port changes; it is deliberately broader than a single port. On Windows the wizard adds the port it read at that moment, so the manual LAN rule below is the one that avoids repeating the work.

The wizard never changes the firewall by itself: it only adds rules after a two-step confirmation, with `a: apply rules` and a second `a`. Any other key cancels the apply step, and `Esc` still quits Pulso. It never removes or replaces existing rules, and it needs no administrator rights just to inspect: when it cannot read the rules, it says so on screen instead of assuming they are fine.

### Manual firewall rules

They match what the wizard proposes, with one exception: on Windows the wizard adds the port it read at that start, while the rule below uses a source-network allow so you do not repeat the step. Replace `192.168.1.0/24` with your own LAN prefix. Where the wizard cannot propose a command yet (firewalld and nftables), these are the manual equivalents.

**ufw (Debian, Ubuntu):** the discovery rule is already present in the default `before.rules` (`5353/udp` to `224.0.0.251`), so what is usually missing is traffic from the LAN:

```sh
sudo ufw allow from 192.168.1.0/24
```

**firewalld (Fedora, RHEL):**

```sh
sudo firewall-cmd --permanent --add-service=mdns
sudo firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="192.168.1.0/24" accept'
sudo firewall-cmd --reload
```

If your firewalld version has no `mdns` service, replace the first line with `sudo firewall-cmd --permanent --add-port=5353/udp`.

**nftables:**

```sh
sudo nft add rule inet filter input udp dport 5353 accept
sudo nft add rule inet filter input ip saddr 192.168.1.0/24 accept
```

Both nftables rules are lost on reboot: write them in `/etc/nftables.conf` and reload with `sudo nft -f /etc/nftables.conf`. The table and chain names depend on your configuration; use the ones from your input chain.

**Windows Defender Firewall,** in a Windows console with administrator rights:

```
netsh advfirewall firewall add rule name="Pulso (Soloist)" dir=in action=allow protocol=UDP localport=5353
netsh advfirewall firewall add rule name="Pulso (Soloist) LAN" dir=in action=allow protocol=TCP remoteip=192.168.1.0/24
```

The first rule opens discovery and matches the one the wizard adds. The second allows TCP traffic from your LAN, which is the part that changes port on every start.

## Connecting Spotify for search and playlists

Search and playlist listing use the **Spotify Web API**, a different authorization from Soloist:

1. Create a free app in the [Spotify developer dashboard](https://developer.spotify.com/dashboard) and copy its **Client ID**. It is 32 hex characters; it is not the Soloist key.
2. In that app's settings, add exactly `http://127.0.0.1:8888/callback` as a redirect URI. Spotify requires the explicit port for local addresses.
3. The wizard asks for the Client ID once and stores it. You can also set `PULSO_SPOTIFY_CLIENT_ID` before launching.
4. Approve access in the browser Pulso opens. Port 8888 must be free; if something else uses it, close that and retry.

After the first approval the session is stored and refreshed automatically. You only approve again if you revoke the app in Spotify or delete the session file. Playback never depends on this authorization: if it fails or you skip it with `s`, controls and audio keep working.

## Daily use

The screen is one workspace:

- **Top bar:** search and your Spotify user.
- **Library:** your playlists.
- **Table:** tracks of the selected playlist, or search results, with number, title, artist and duration.
- **Playing bar:** track, artist, album, progress, shuffle, repeat, volume and device.
- **Footer:** `key action` pairs for the focused panel; `F1` opens help.

`Tab` cycles library, table, search and playing bar. With the search field focused you can type `q`, `s`, `r`, `+` and `-` without triggering shortcuts; `Enter` searches and `Esc` leaves the field.

Quitting Pulso pauses playback. Soloist stays in the background with its pairing so the next launch is instant; stop that process from the system if you want it gone.

## Shortcuts

| Key | Action |
| --- | --- |
| `Tab` / `Shift+Tab` | Change panel |
| `↑` `↓` / `j` `k` | Move the selection |
| `Enter` | Open a playlist, search, or play the selection |
| `Space` | Play or pause |
| `n` / `p` | Next or previous |
| `←` `→` | Seek 10 seconds (playing bar) |
| `s` | Shuffle |
| `r` | Repeat: off, context, track |
| `+` / `-` | Volume |
| `/` | Search |
| `l` | Jump to the library |
| `F1` / `?` | Help with every control |
| `F2` | English or Spanish |
| `F3` | Standard, dark or light theme |
| `q` / `Esc` | Quit |

`F1`, `?`, `Esc` or `q` close help and return to the panel you came from.

## Language and themes

`F2` switches English and Spanish; `F3` cycles standard, dark and light. The choice is stored only when you press one of those keys and applies to both the wizard and the player.

## Where data lives

Everything lives under your user configuration directory (`%APPDATA%\pulso` on Windows, `~/.config/pulso` on Linux):

| File | Contents |
| --- | --- |
| `client-id` | Public Client ID of your Spotify app |
| `spotify-session.json` | Spotify session: access token, refresh token and expiry |
| `preferences.json` | Language and theme |
| `bin/soloist.exe` or `bin/soloist` | Soloist copy downloaded by Pulso |

Files are created with private permissions. Pulso never writes tokens to logs or output. Audio and pairing belong to Soloist, which keeps them in its own directory: `%LOCALAPPDATA%\soloist\data` on Windows, or `$XDG_DATA_HOME/soloist` (`~/.local/share/soloist` by default) on Linux.

## Uninstalling

`pulso uninstall` deletes what Pulso and Soloist keep in your user account. It first prints the absolute path of every target and then waits for a typed confirmation: you must type exactly `uninstall`. A bare Enter or any other word answers "Cancelled; nothing was deleted." and exits 1 without deleting anything.

When Soloist is running the command never deletes on its own: it says so and asks `Close it and continue? [y/N]`. Only `y` or `Y` checks that the pid in the marker really is Soloist and, when it is, asks the daemon to stop and waits for it to exit; if it is still alive after the wait, the command aborts without deleting anything. When it cannot confirm that process is Soloist it sends no signal at all and aborts. It never sends `SIGKILL`; on Windows it uses the platform's process termination.

It removes the Soloist binary Pulso downloaded (`bin/soloist` or `bin/soloist.exe`), the Client ID, the Spotify session, the preferences and Soloist's data and cache directories, which hold the pairing and its cache.

Each target is reported as `Removed`, `Absent` or `Failed` with the error. A target that is already gone is not a problem: running the command twice finishes well. If one deletion fails, it is reported and the command continues with the rest, but the exit code is 1; it is 0 only when every target was removed or absent.

The pairing with Spotify and the Web API authorization cannot be recovered: after uninstalling you have to repeat both, with the Soloist key and with the Client ID. The command does not delete the Pulso binary either: it prints its path so you can delete it yourself, because `go install` owns it.

```sh
bin="$(go env GOBIN)"; [ -n "$bin" ] || bin="$(go env GOPATH)/bin"
rm "$bin/pulso"
```

On Windows the binary is `pulso.exe` in the same folder. If a path does not belong to Pulso or Soloist, is your home directory, the system root or the directory you are working in, the command refuses to delete and exits without touching anything.

## Diagnostics and troubleshooting

`pulso doctor` reports Soloist's daemon, WebSocket and sign-in state without installing or starting anything. Use `pulso doctor --data-dir PATH` when your instance keeps its data elsewhere.

| Symptom | What to check |
| --- | --- |
| The device does not appear in Spotify | Same local network, no VPN or guest isolation; on Windows, allow Soloist through the firewall |
| No sound | System audio output; on Linux, PipeWire or PulseAudio |
| The Client ID is requested again | Check `client-id`; it must be 32 hex characters |
| Spotify rejects the redirect URI | It must be exactly `http://127.0.0.1:8888/callback` |
| Port 8888 is busy | Close whatever uses it and retry |
| Authorization error | Revoke the app in Spotify, delete `spotify-session.json` and approve again |
| Soloist exits with code 10 | The build expired after 90 days; the wizard offers an update |
| Empty playlist | Spotify only exposes tracks of playlists you own or collaborate on; you can still play it whole |

## Tests

```sh
go test ./...
go vet ./...
```

Tests use local fixtures and fake transports: no real Spotify requests and no credentials.

## Known limits

- No playlist editing, podcasts, lyrics, visualizer or history.
- Does not work without Spotify Premium.
- Only plays on platforms Spotify ships Soloist for.
- Search returns ten results per page and requires paging.
- The progress bar interpolates between Soloist reports; it can drift by under a second.
- The first pairing requires the official Spotify app once.

## Repository layout

```
cmd/pulso      entry point
internal/app   code and tests
docs           plan, roadmap and UI plan
odd            feature document and its log
```

Contributor and agent rules live in [AGENTS.md](AGENTS.md).

## Uninstalling

`pulso uninstall` deletes what Pulso and Soloist keep in your user account. It first prints the absolute path of every target and then waits for a typed confirmation: you must type exactly `uninstall`. A bare Enter or any other word answers "Cancelled; nothing was deleted." and exits 1 without deleting anything.

When Soloist is running the command never deletes on its own: it says so and asks `Close it and continue? [y/N]`. Only `y` or `Y` checks that the pid in the marker really is Soloist and, when it is, asks the daemon to stop and waits for it to exit; if it is still alive after the wait, the command aborts without deleting anything. When it cannot confirm that process is Soloist it sends no signal at all and aborts. It never sends `SIGKILL`; on Windows it uses the platform's process termination.

It removes the Soloist binary Pulso downloaded (`bin/soloist` or `bin/soloist.exe`), the Client ID, the Spotify session, the preferences and Soloist's data and cache directories, which hold the pairing and its cache.

Each target is reported as `Removed`, `Absent` or `Failed` with the error. A target that is already gone is not a problem: running the command twice finishes well. If one deletion fails, it is reported and the command continues with the rest, but the exit code is 1; it is 0 only when every target was removed or absent.

The pairing with Spotify and the Web API authorization cannot be recovered: after uninstalling you have to repeat both, with the Soloist key and with the Client ID. The command does not delete the Pulso binary either: it prints its path so you can delete it yourself, because `go install` owns it.

```sh
bin="$(go env GOBIN)"; [ -n "$bin" ] || bin="$(go env GOPATH)/bin"
rm "$bin/pulso"
```

On Windows the binary is `pulso.exe` in the same folder. If a path does not belong to Pulso or Soloist, is your home directory, the system root or the directory you are working in, the command refuses to delete and exits without touching anything.

