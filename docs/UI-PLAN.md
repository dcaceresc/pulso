# Pulso workspace UI plan

Turn the current playback screen plus modal overlays into one keyboard-first workspace: search on top, library on the left, a song table in the middle, and a live now-playing bar above the key hints. Unicode glyphs only, playlists first.

## Target layout

```
┌ Pulso ─────────────────────────────────────────────────────────────────┐
│ Buscar: ______________________                          @usuario        │
├──────────────────┬─────────────────────────────────────────────────────┤
│ BIBLIOTECA       │ Rock Chileno & Clásicos · 42 canciones              │
│ > Playlists      │  #   Título                 Artista          Duración│
│   Playlist A     │  ───────────────────────────────────────────────────│
│   Playlist B     │  1   Tren al Sur            Los Prisioneros    5:36 │
│   Playlist C     │ >2   Estrechez de Corazón   Los Prisioneros    6:21 │
│                  │  3   Llueve sobre la Ciudad Los Bunkers        3:56 │
├──────────────────┴─────────────────────────────────────────────────────┤
│ ▶ Estrechez de Corazón · Los Prisioneros   ⇄ off  ↻ all  vol 85%       │
│ 2:14 ━━━━━━━━━━╸──────────────── 6:21                  Dispositivo Desk│
├────────────────────────────────────────────────────────────────────────┤
│ Tab: panel  Enter: reproducir  Space: pausa  /: buscar  F2/F3  q: salir │
└────────────────────────────────────────────────────────────────────────┘
```

- Wide terminals (≥100×30): three regions, sidebar 26–32 columns, table flexible.
- Breakpoint (80×24): sidebar collapses to a one-row library switcher; table keeps title and artist.
- Narrow (<80): only focus and playing track; keys move between views.
- One accent, glyph roles `▶ ⏸ ⏮ ⏭ ⇄ ↻ ●` and `[x]`-free state words; no color-only meaning.
- Every row is budgeted: no blank spacer rows between regions, one rule per separation.

## Interaction model

| Key | Action |
| --- | --- |
| `Tab` / `Shift+Tab` | Cycle sidebar → table → search → now-playing bar |
| `↑` `↓` / `j` `k` | Move selection in the focused region |
| `Enter` | Play selection or open the playlist under the cursor |
| `/` | Focus search, `Enter` submits, `Esc` cancels typing only |
| `Space` | Play/pause |
| `n` `p` | Next/previous track |
| `←` `→` | Seek 10 s inside the now-playing bar |
| `+` `-` | Volume |
| `s` | Toggle shuffle for current playback |
| `S` | Start and shuffle the selected or open playlist by URI, including restricted playlists; not available in search results |
| `a` | Save the playing track to a chosen playlist: `↑` `↓` / `j` `k` choose, `Enter` saves, `Esc` cancels; only with a writable catalog and a track URI from Soloist |
| `x` | Remove the highlighted track from the open playlist's track page: the prompt names the track and the playlist, `Enter` confirms, `Esc` cancels; only with a writable catalog, a track URI and the listing's snapshot |
| `r` | Cycle repeat: off → context → track |
| `F2` / `F3` | Language / theme |
| `Esc` | Leave the focused input or panel, then quit |
| `q` | Quit when no text input is focused |

## Data and gaps

| UI element | Source | Status |
| --- | --- | --- |
| Playlist names | `GET /v1/me/playlists` | exists |
| Playlist tracks and durations | `GET /v1/playlists/{id}/items` | exists; `total` needs keeping, restricted playlists need the existing fallback |
| Search results | `GET /v1/search?type=track` | exists, currently the only view |
| User handle | `GET /me` `display_name` | new, no extra scope |
| Shuffle, repeat, device, volume | Soloist `playback_state.options` and `device_changed` | partially mapped; add shuffle/repeat |
| Confirmed playlist context | Soloist `playback_state.context` and `context_changed` | show only a validated playlist entity, independent of the library selection |
| Seek | `seek` command, `position_sync` | transport exists, UI key is new |
| Skipping to a track in a playlist | `play` with URI starts the context | exists |
| Saving the playing track | `POST /v1/playlists/{id}/items` with `uris` and Soloist's `item.uri` | new; needs `playlist-modify-private` and `playlist-modify-public`, recovered with one explicit reauthorization when an older session approved read-only or the refusal names no cause |
| Removing a playlist track | `DELETE /v1/playlists/{id}/items` with `items` (the row's URI) and the listing's `snapshot_id` | new; removes one occurrence, reads the listing again when Spotify reports a stale snapshot, and reuses the same single explicit reauthorization |

Playlist shuffle sends `play` with the playlist URI, waits for acceptance and confirmation of that playlist on the current Soloist session, then sends `set_shuffle`. An intervening observed change away from the confirmed context cancels the pending action. The commands are not atomic: an unobserved context switch between confirmation and command execution can still affect different playback. The first track is not guaranteed to be random. Spotify manages Autoplay; Pulso does not modify or simulate it.

Save to playlist reads Soloist's `item.uri` and keeps it only when it is a track URI, so a missing or non-track item leaves the action unavailable instead of saving the wrong thing. Both write paths use Spotify's current playlist-items route: adding keeps the `uris` body and removing sends `items` with the listing's `snapshot_id`. The chooser reuses the playlist page the library already loads, including its paging; `Enter` sends one request and the success notice appears only after Spotify accepts it. A `403` that does not clearly state that this account cannot change that particular playlist offers one explicit reauthorization with the wizard's browser-and-link presentation and retries the save once, because reapproving can fix a missing grant; a refusal that names the playlist the account cannot edit is reported as such without a retry. Any other failure is one non-blocking notice named for the playlist, with no cause invented. Playback is never touched and Spotify response bodies are never rendered.

Remove from playlist is the mirror image on the browsed playlist's track page: every row carries the snapshot of the listing it came from, `x` asks for an explicit confirmation that names the track and the playlist, and only the chosen occurrence is sent. Because the snapshot describes the listing the person acted on, a duplicated track loses exactly one entry. When Spotify reports that the snapshot is stale, Pulso reads the playlist again and asks again instead of guessing which occurrence to drop; a missing write scope, and any rejection that does not clearly name a playlist this account cannot edit, reuses the same single explicit reauthorization and retry. Success names the track and the playlist and reloads the listing so the row disappears; any other failure is one non-blocking notice. Playback, the queue and Spotify response bodies stay untouched.

Albums, artists, liked songs and podcasts are still out of this slice: they need new scopes and re-authorization.

## Slices

| Slice | Content | Acceptance |
| --- | --- | --- |
| U1 Shell | Frame, top bar, sidebar, table region, now-playing bar, key hints, focus model, `Tab` routing, i18n and theme applied | Renders inside 120×40, 100×30, 80×24 and 40×12 with no overflow; focus cycles and never traps typing; `q` quits only outside text input |
| U2 Library | Sidebar playlists from the Web API, table of tracks with `#`, title, artist, duration, header with total, restricted-playlist fallback, loading and empty states | Selecting a playlist loads its tracks; `Enter` plays it; a restricted playlist says so and still plays by URI; paging reaches more than one page |
| U3 Playing bar | Title, artist, progress interpolated from `position_sync`, shuffle/repeat/volume/device, seek and mode keys wired to Soloist | Bar reflects Soloist state pushed from another device within one event; seek lands within a second; shuffle/repeat round-trip |
| U4 Search | `/` focuses the top bar, results replace the table, `Enter` plays, `Esc` returns to the playlist | Search is reachable from any panel; returning restores the previous table and cursor |
| U5 Polish | Narrow layouts, empty/error copy in both languages, palette contrast, keyboard hints that follow focus, removal of the old modal overlay | All prior tests still pass; no emoji; each state has a glyph or word, not color alone |
| U6 Save | `a` opens a chooser over the library's playlist page, one save request, success notice only after Spotify accepts, one explicit reauthorization for a missing write scope | Chooser names the targeted playlist, `Enter` saves, `Esc` cancels; a read-only catalog hides the action; failures stay non-blocking and playback is untouched |
| U7 Remove | `x` on a playlist's track page asks to remove the highlighted track, sending the listing's snapshot so only the chosen occurrence goes | The prompt names the track and playlist; `Enter` confirms and `Esc` cancels; a stale snapshot reads the listing again and asks again, a missing write scope reuses the one explicit reauthorization, the list reloads after success and playback is untouched |

Each slice is one reviewable commit; U1–U3 are the visible change, U4 removes the overlay, U5 closes the polish, U6 adds saving and U7 adds removal, none of them touching playback.

## Constraints and risks

- Playlist contents stay limited to owned or collaborative playlists in Spotify development mode; the fallback is part of the design, not an error.
- Saving a track depends on the Web API write scopes: an older read-only session is recovered with one explicit reauthorization, and any rejection that does not clearly name a playlist the account cannot edit offers that same single reauthorization instead of claiming the playlist cannot be changed. Only a refusal that names the missing permission on that playlist is reported without a retry.
- Removing a track needs the snapshot of the playlist listing that produced the row; a stale snapshot makes Pulso read the listing again and ask again instead of guessing which occurrence to drop, and duplicates are removed one at a time. The rows of a browsed playlist carry that snapshot, so a search result or a catalog that cannot write offers no removal.
- Search is capped at 10 results per request; the table must page instead of truncating silently.
- Seek and mode commands are best-effort: Soloist confirms dispatch, not the resulting state; the bar must show the event, never an optimistic guess for longer than one event.
- Text entry must swallow `q`, `s`, `r`, `+` and `-` so a search query can contain them.
- Unicode glyph width is fixed at one cell; no emoji, no CJK in chrome.
- Keep the transport, setup wizard, pause-on-exit, preferences and theme system unchanged; the UI consumes them.
