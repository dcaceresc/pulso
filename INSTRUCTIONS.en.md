# Pulso instructions

English · [Español (predeterminado)](INSTRUCTIONS.md)

This guide explains how to install, configure and use Pulso. The app targets my personal workflow: listening to Spotify from the terminal on Windows and Linux through Spotify's official player instead of `librespot`.

## Contents

- [Recommended install](#recommended-install)
- [First launch](#first-launch)
- [Pairing Soloist](#pairing-soloist)
- [Connecting Spotify for search, playlist listing and adding or removing tracks](#connecting-spotify-for-search-playlist-listing-and-adding-or-removing-tracks)
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

In the pairing step the wizard diagnoses why the phone may not see the computer and summarises the result with an indicator at the start of the line: `● OK` in green when there is nothing to review, `● Review` in amber when there is a risk but pairing can still work (a container bridge competing for the announcement, for example), and `● Blocked` in red when the diagnosis identifies a policy or rule that prevents access. The word always accompanies the colour, so it still reads on a terminal without colour.

The `d: diagnostics` shortcut appears only when there is something to review: it opens the detail with the findings and, when there is something to apply, the exact commands. `Enter` returns to the pairing step. When the detail can propose no command, it says so and offers no `a`.

The diagnosis checks three things:

- The address your machine uses to reach the local network.
- The interfaces that compete for the single address Soloist announces: container bridges (Docker's, for example), VPNs and tunnels.
- The firewall state and any blocks the diagnosis can identify.

A bridge or a VPN breaks pairing because Soloist announces one address at startup and offers no option to pin the interface: it can advertise the bridge address instead of your LAN address, and the phone searches the wrong network. The concrete recovery is to start Pulso with that bridge absent, pair, and bring the bridge back afterwards. The advertisement stays valid until Soloist restarts.

Soloist's control port is chosen at startup and changes, so a rule for one port goes stale on the next start. What is recommended is a LAN-scoped allow (for example `sudo ufw allow from 192.168.1.0/24`), which keeps working after the port changes; it is deliberately broader than a single port. On Windows, the wizard reads the effective policy for the LAN interface profile. It marks discovery (UDP 5353) as blocked only if it confirms the profile blocks unsolicited inbound connections across the board. Otherwise it shows “Not verified”; this is not a connectivity test. Soloist's local WebSocket does not reveal the Spotify Connect TCP port, which is shown as unknown.

The wizard never changes the firewall by itself: it only adds rules after a two-step confirmation, with `a: apply rules` and a second `a`. Any other key cancels the apply step, and `Esc` still quits Pulso. It never removes or replaces existing rules, and it needs no administrator rights just to inspect: when it cannot read the rules, it says so on screen instead of assuming they are fine.

### Manual firewall rules

Replace `192.168.1.0/24` with your own LAN prefix. For firewalld and nftables, where the wizard does not propose commands, the manual equivalents are listed below.

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

**Windows Defender Firewall:** the wizard does not offer to change rules. Adding an exception cannot fix a policy that ignores inbound allow rules. If a block is confirmed and the device does not appear in Spotify, ask whoever administers the computer to review the LAN profile policy. If pairing works, do not change the firewall because of a “Not verified” status.

## Connecting Spotify for search, playlist listing and adding or removing tracks

Search, playlist listing and adding or removing a track in a playlist use the **Spotify Web API**, a different authorization from Soloist:

1. Create a free app in the [Spotify developer dashboard](https://developer.spotify.com/dashboard) and copy its **Client ID**. It is 32 hex characters; it is not the Soloist key.
2. In that app's settings, add exactly `http://127.0.0.1:8888/callback` as a redirect URI. Spotify requires the explicit port for local addresses.
3. The wizard asks for the Client ID once and stores it. You can also set `PULSO_SPOTIFY_CLIENT_ID` before launching.
4. Approve access in the browser Pulso opens. Port 8888 must be free; if something else uses it, close that and retry.

After the first approval the session is stored and refreshed automatically. You only approve again if you revoke the app in Spotify or delete the session file. Adding or removing a track also needs write permission: an older approval that only allowed reading does not have it, and Pulso offers to approve again at the moment you add or remove. When Spotify rejects the change without a recognizable cause, Pulso offers that same single approval instead of claiming the playlist cannot be changed; only when Spotify clearly states that the account cannot change that particular playlist does the notice repeat it. Playback never depends on this authorization: if it fails or you skip it with `s`, controls and audio keep working.

## Daily use

The screen is one workspace with three zones and the player at the bottom:

- **Top bar:** search and your Spotify user.
- **Library:** your playlists, with the cursor and the `l` key that brings the focus there.
- **Table:** tracks of the selected playlist, search results, or the queue, with number, title, artist and duration.
- **Queue:** in wide windows a third box shows what comes next and the playback context; `c` brings the queue back into the table and gives it the focus.
- **Player:** a box at the bottom. Its top border carries the state — playing, paused, disconnected —, shuffle, repeat and volume; inside are the cover of the playing track — or a block with its initials when there is no image —, the title, the artist, the album, the time with its bar and the playlist or remote device Soloist reports.

Focus moves only with the zone keys: `l` goes to the library, `/` to search and `c` to the queue. Inside the workspace `Esc` never quits Pulso: it returns to the table or cancels what has the focus; the wizard, in turn, does quit on `Esc`. There is no focus cycle and no key hint line at the bottom either: every zone has its letter, and `?` opens help, the only key reference, which lists them by scope — global, zones, library, table and search.

The window size decides the shape of the screen; the width conditions are floors and the height decides: when the height misses a band, Pulso uses the next richer shape that does fit, never the poorest. That is why 120×29 uses the medium band and 100×25 the compact one.

| Size | What you see | Player | Cover |
| --- | --- | --- | --- |
| width ≥ 120 and height ≥ 30 | library, table and queue with its context | 10 interior rows | yes |
| width ≥ 100 and height ≥ 26, also above 120 columns when the height misses the wide band | library and table; the queue opens with `c` | 10 rows | yes |
| width ≥ 80 and height ≥ 20 | one library row above the table | 8 rows | yes |
| width ≥ 40 and height ≥ 12 | the library row and the player, and the table when the height leaves rows for its header | 6 rows | no |
| width < 40 or height < 12 | one message with the minimum size, no frame | — | — |

The table is the zone that gives up rows and no row overflows the width. In the minimum band the player box declares six interior rows and a low window crops them — at 40×12 it draws three — so its bottom border is never lost. Only the bands with zones, wide and medium, promote a session sentence that does not fit inside its box — a long authorization notice, for example — to the full width; in the compact and minimum bands the notice is an ordinary row and, when it is wider than the window, it is clipped.

`a` opens the playlist chooser for the playing track: `↑` `↓` or `j` `k` choose the playlist, the panel title names the targeted playlist, `Enter` saves and `Esc` cancels. The success notice appears only once Spotify confirmed the playlist, and a failure shows as one row that does not interrupt playback.

`x` asks to remove the highlighted track from the open playlist: the confirmation names the track and the playlist, `Enter` removes it and `Esc` cancels. When Spotify accepts, the notice names the track and the playlist, and the playlist is read again so the row disappears. Playback and the queue are never touched.

With the search field focused you can type `q`, `s`, `r`, `+` and `-` without triggering shortcuts; `Enter` searches and `Esc` leaves the field. `q` and `Ctrl+C` quit Pulso: `q` quits while no text field has the focus and `Ctrl+C` always quits.

Quitting Pulso pauses playback. Soloist stays in the background with its pairing so the next launch is instant; stop that process from the system if you want it gone.

## Shortcuts

Every key does one thing. `?` opens help, the complete reference by scope: it repeats this table and adds the search field's own keys and `s` as a global shuffle key for the current playback.

| Scope | Key | Action |
| --- | --- | --- |
| Global | `q` / `Ctrl+C` | Quit |
| Global | `Space` | Play or pause |
| Global | `n` / `b` | Next or previous track |
| Global | `+` / `-` | Volume |
| Global | `←` / `→` | Seek back or forward 10 seconds |
| Global | `r` | Repeat: off, context, track |
| Global | `F2` / `F3` | Language or theme |
| Global | `?` | Help: the only key reference |
| Global | `Esc` | Back to the table or cancel what has the focus |
| Zone | `l` | Library |
| Zone | `/` | Search: `q`, `s`, `r`, `+` and `-` are text |
| Zone | `c` | The table shows the queue and takes the focus; the wide window's column is informative only |
| Library | `j` / `k`, `Enter` | Move the cursor, open the playlist |
| Library | `s` | Play the selected playlist shuffled |
| Table | `j` / `k`, `Enter` | Move the cursor, play the row |
| Table | `a` / `x` | Save the playing track or remove the highlighted row |

The arrows `↑` `↓` move the cursor just like `j` `k`. `s` toggles shuffle for the current playback while the focus is outside the library; there it plays the selected playlist shuffled. A restricted playlist, whose tracks Spotify does not hand over, plays with `Enter`.

`?` or `Esc` close help and return to the zone you came from.

`s` in the library works by playlist URI even when the tracks cannot be listed. Pulso waits for Soloist to accept playback and confirm the playlist context before sending a separate shuffle command. The first track is not guaranteed to be random, and another Spotify client can change the context between confirmation and shuffle. If confirmation does not arrive in time, Pulso does not send shuffle; the playlist may still start playing.

Pulso does not change Autoplay. If it is enabled and Spotify continues playback after a playlist ends, Spotify chooses the tracks.

`a` appears only with a track playing and a Spotify catalog that can write. After `Enter`, Pulso waits for Spotify's answer: when it accepts, it shows “Saved to …” with the playlist name; on failure it shows one row such as “Could not save to …” and playback stays as it was. When Spotify answers that the write permission is missing (for example, a session approved before this action existed), Pulso offers to approve again, opens the browser, shows the approval link and retries the save once. A playlist this account cannot edit, or an approval that does not complete, gets no retry. Spotify's response text is never shown.

`x` appears only with a playlist open in the table, a row with a track URI and the snapshot of the playlist you were looking at, which Pulso reads from the playlist object because the items page does not return it; when Spotify does not return one, the key does not appear. In search results, in a restricted playlist or without a catalog that can write the key does nothing. Pulso removes exactly one occurrence: the one that belongs to the snapshot of the listing you were looking at, so a duplicated track keeps its other occurrences and is removed one at a time. After `Enter` it shows “Removed … from …” and reads the playlist again; a failure stays as one row such as “Could not remove … from …”. When Spotify answers that the listing changed since it was read, Pulso reads it again and asks again with the new listing instead of guessing which occurrence to drop. A missing write permission offers the same approval as saving and retries once; a playlist this account cannot edit, or an approval that does not complete, gets no retry. Spotify's response text is never shown, and playback and the queue stay as they were.

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
| The device does not appear in Spotify | Same local network, no VPN or guest isolation; on Windows, check the pairing diagnostics |
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

- Does not create, rename or delete playlists: it appends the playing track to an existing playlist and removes one occurrence of a track from the open playlist, always where your account can write.
- Does not save tracks without the Web API write permission, and Spotify development mode only allows editing playlists you own or collaborate on.
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

