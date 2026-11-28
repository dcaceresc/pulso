# Pulso workspace UI

The workspace as it ships: one screen, three zones — library, table and queue — and the player as a box at the bottom whose top border carries the play state, shuffle, repeat and volume. Keyboard first, Unicode glyphs only, playlists first.

## Layout

The screen is two header rows, one rule, the zones, one rule and the player box. Every row measures the window width exactly; the table is the only zone that gives up rows.

- **Banner:** `Pulso` on the left and the session word — playing, paused, connected, disconnected or loading — on the right.
- **Search row:** the `Search:` field with its cursor or its placeholder, and the Spotify account label (`@name`) on the right until it does not fit.
- **Zones:** each one is a box with its title on the top border and an accented border while it has the focus. The library box lists playlists with the owner when the name leaves room; the table box names what it shows — the open playlist and its total, the submitted query and its total, or `Playback` with the queue length — and carries the number, title, artist and duration columns; the queue box lists what comes next, each track with its artist below it, and ends with the `CONTEXT` block: the playlist that plays — or the open one when nothing is playing — and the shuffle and repeat modes.
- **Player box:** the top border carries the status line; inside are the cover — or the initials block when there is no image — the title, the artist, the album, the time line `elapsed / total (remaining)` right under the album, the bar on the next row, which fills every cell up to the box's right edge, and a context line naming the playlist, which names the device too only when playback runs on another one. The session notice stays as one row under the box.

In the bands that have zones — wide and medium — a session sentence that does not fit inside its zone — a long authorization or confirmation notice — is promoted to the full width above the player box, wrapped rather than truncated, and the zone keeps only its state mark. The compact and minimum bands have no boxes: there the notice is an ordinary full-width row, and one wider than the window is clipped.

The mockups below are the app's own render at the size each heading names, not a drawing of an intended screen.

### Wide band — 120×34

Library (25 columns), the table (the rest) and the queue (30 columns) share one height, from 120 columns and 30 rows up.

```
Pulso                                                                                                            playing
Search: type to search                                                                                             @user
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
┌─ LIBRARY ─────────────┐ ┌─ Rock Chileno · 42 tracks ──────────────────────────────────┐ ┌─ UP NEXT ──────────────────┐
│> Rock Chileno · ana   │ │#  Title                    Artist                   Duration│ │1 Huelga de Dolores         │
│  Jazz · beto          │ │─────────────────────────────────────────────────────────────│ │  Los Miserables            │
│                       │ │                                                             │ │                            │
│                       │ │>1 Tren al Sur              Los Prisioneros          5:36    │ │                            │
│                       │ │ 2 Estrechez de Corazón     Los Prisioneros          6:21    │ │                            │
│                       │ │ 3 Llueve sobre la Ciudad   Los Bunkers              3:56    │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │CONTEXT                     │
│                       │ │                                                             │ │Rock Chileno                │
│                       │ │                                                             │ │⇄ on · ↻ all                │
└───────────────────────┘ └─────────────────────────────────────────────────────────────┘ └────────────────────────────┘
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
                                                                                                                        
├─ ▶ Playing  ▸  ⇄ on  ▸  ↻ all  ▸  Volume 37% ────────────────────────────────────────────────────────────────────────┤
│                        Estrechez de Corazón                                                                          │
│  ┌──────────────────┐  Los Prisioneros                                                                               │
│  │                  │  La Cultura de la Basura                                                                       │
│  │                  │  2:14 / 6:21 (-4:07)                                                                           │
│  │                  │  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━╸────────────────────────────────────────────────────────────│
│  │                  │                                                                                                │
│  │        EC        │                                                                                                │
│  │                  │                                                                                                │
│  │                  │                                                                                                │
│  │                  │                                                                                                │
│  └──────────────────┘                                                                                                │
│                        Playlist: Rock Chileno                                                                        │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Medium band — 100×30

From 100 columns and 26 rows: the same two boxes as the wide band without the queue column, so the table takes its width. `c` brings the queue into the table and gives it the focus.

### Compact band — 80×24

From 80 columns and 20 rows: no boxes. A one-row library switcher names the current playlist and the `l` key, and the table drops from beneath it.

```
Pulso                                                                    playing
Search: type to search                                                     @user
────────────────────────────────────────────────────────────────────────────────
LIBRARY: Rock Chileno                                                  l library
Rock Chileno · 42 tracks                                                        
#  Title                             Artist                             Duration
────────────────────────────────────────────────────────────────────────────────
>1 Tren al Sur                       Los Prisioneros                    5:36    
 2 Estrechez de Corazón              Los Prisioneros                    6:21    
 3 Llueve sobre la Ciudad            Los Bunkers                        3:56    
                                                                                
                                                                                
────────────────────────────────────────────────────────────────────────────────
                                                                                
├─ ▶ Playing  ▸  ⇄ on  ▸  ↻ all  ▸  Volume 37% ────────────────────────────────┤
│  Estrechez de Corazón                                                        │
│  Los Prisioneros                                                             │
│  La Cultura de la Basura                                                     │
│  2:14 / 6:21 (-4:07)                                                         │
│  ━━━━━━━━━━━━━━━━━━━━━━━━━━╸─────────────────────────────────────────────────│
│                                                                              │
│                                                                              │
│  Playlist: Rock Chileno                                                      │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Minimum band — 40×12

From 40 columns and 12 rows: the library switcher and the player box, and the table too when the height leaves rows for its header and one track; at 40×12 it does not, so only the switcher row is drawn.

```
Pulso                            playing
Search: type to search             @user
────────────────────────────────────────
LIBRARY: Rock Chileno          l library
                                        
────────────────────────────────────────
                                        
├─ ▶ ▸ ⇄ on ▸ ↻ all ▸ Volume 37% ──────┤
│  Estrechez de Corazón                │
│  Los Prisioneros                     │
│  La Cultura de la Basura             │
└──────────────────────────────────────┘
```

### Insufficient band

Below 40 columns or 12 rows one centred row says `Minimum 40×12 · now 39×11`; there is no frame.

## Sizes (one matrix)

The width conditions are floors and the height decides, so the first band in the ladder whose floor the window reaches wins: a height that misses a band falls to the next richer shape that fits, never to the poorest. That is why 120×29 uses the medium band and 100×25 the compact one. This matrix is the single source of truth and is copied from the feature document.

| Band | Condition | Zones | Player box | Cover | Proof |
| --- | --- | --- | --- | --- | --- |
| Wide | width ≥ 120 and height ≥ 30 | library + table + queue/context | 12 interior rows | yes | 140×40, 120×34 |
| Medium | width ≥ 100 and height ≥ 26, also above 120 columns when the height misses the wide band | library + table; queue with `c` | 12 rows | yes | 100×30 |
| Compact | width ≥ 80 and height ≥ 20 | library switcher + table | 8 rows | no | 80×24 |
| Minimum | width ≥ 40 and height ≥ 12 | library switcher and the player box, and the table when the height leaves rows for its header | 6 rows | no | 40×12, 79×23 |
| Insufficient | width < 40 or height < 12 | size message, no frame | — | — | 39×11 |

Rules that do not change with the band: the status line always lives on the player box's top border; inside, the title comes first, then the artist, the album, the time line and the bar, and the context line closes the box; the bar is drawn only when its row keeps at least four cells, and below that it is not drawn and the row stays empty while the time line still carries the three values; the box never grows past its band height and, when the window is short, is cropped below it so the bottom border is never lost; the table cedes its rows first; no row overflows the width.

## Controls (one key, one action)

`q` is the application exit: it only quits while no text field has the focus; `Ctrl+C` always quits. Inside the workspace `Esc` never quits: it returns to the table or cancels what has the focus, and the setup wizard is the one surface that still exits on `Esc`.

| Scope | Key | Action |
| --- | --- | --- |
| Global | `q` / `Ctrl+C` | Quit |
| Global | `Space` | Play / pause |
| Global | `n` / `b` | Next / previous track |
| Global | `+` / `-` | Volume |
| Global | `←` / `→` | Back / forward 10 s |
| Global | `r` | Repeat: off → all → one |
| Global | `F2` / `F3` | Language / theme |
| Global | `?` | Help: the only key reference |
| Global | `Esc` | Back to the table or cancel what has the focus |
| Zone | `l` | Library |
| Zone | `/` | Search (the field swallows `q`, `s`, `r`, `+`, `-`) |
| Zone | `c` | The table shows the queue and takes the focus (the wide band's column is informative only) |
| Library | `j` / `k`, `Enter` | Move the cursor, open the playlist |
| Library | `s` | Play the selected playlist shuffled |
| Table | `j` / `k`, `Enter` | Move the cursor, play the row |
| Table | `a` / `x` | Save the playing track / remove the highlighted row |

`?` opens the full-screen help, the only key reference: it groups every control by scope — global, zones, library, table, search — and `?` or `Esc` closes it. Focus moves only with the zone keys: there is no focus cycle and no hint line. `s` outside the library is shuffle for the current playback; in the library it plays the selected playlist shuffled.

## Cover

The cover fills a slot of 20 columns by 10 rows: half blocks `▀` with 24-bit colour, where each cell paints two pixels of a 20×20 reduction of the album image — 400 pixels, four times the previous 10×10 slot. The initials block — two letters of the title in a box — stands in whenever there is no image.

- The URL comes from `album.images` in the responses the app already requests (`/v1/search` and `/v1/playlists/…/items`); the playing track that no loaded page brought is resolved with one narrow `GET /v1/tracks/…?fields=album.images`. The image picked is the smallest one at least as wide as the drawn cover — 20 pixels —, and the largest available when none reaches it.
- Download and decode use `image/png` and `image/jpeg` from the standard library: no new dependency and no new scope. The download is capped at 4 MiB and the header at one million pixels before decoding; the image is reduced by block average and drawn with 24-bit colour.
- The first paint shows the initials block and never waits for the image; the result is cached in memory per URL and a failed download is remembered as well, leaving the initials with no notice and no error row.
- The slot rule is one and shared by drawing and requesting — at least 80 columns and 12 interior rows, the ten of the image plus the title above and the context below — so a window without a slot downloads nothing, and a window that grows into the slot asks for the image then. That is why the cover exists in the wide and medium bands and not in the compact or minimum ones.

## Data

| Element | Source | State |
| --- | --- | --- |
| Playlists and owners | `GET /v1/me/playlists` | delivered, paged |
| Tracks, durations and total | `GET /v1/playlists/…/items` | delivered, paged; a restricted playlist says its contents are unavailable and plays by URI with `Enter` |
| Playlist snapshot for removal | `GET /v1/playlists/…?fields=snapshot_id` | delivered; the items page does not return it, so it is read from the playlist object next to the page, and a missing snapshot only leaves `x` unavailable |
| Search results | `GET /v1/search?type=track` | delivered, ten per request |
| Account handle | `/me` `display_name` | delivered; a missing name shows no label |
| Shuffle, repeat, volume and playing state | Soloist `playback_state.options` and its events | delivered; the box shows what Soloist reported, never an optimistic guess beyond the pending intent |
| Confirmed playlist context | Soloist `playback_state.context` and `context_changed` | delivered; only a validated playlist entity is named |
| Queue | Soloist `get_queue` | delivered; the wide band's column and the table's `c` view read it |
| Seek | `seek` command and `position_sync` | delivered; the bar moves at once and Soloist corrects it |
| Saving the playing track | `POST /v1/playlists/…/items` with `uris` and Soloist's `item.uri` | delivered; needs the write scopes, recovered with one explicit reauthorization |
| Removing a playlist track | `DELETE /v1/playlists/…/items` with `items` and the playlist's `snapshot_id` | delivered; removes one occurrence, reads the listing again on a stale snapshot and reuses the same single reauthorization |
| Album cover | `album.images` in the responses above, plus one narrow track query | delivered; standard library decode, 24-bit half blocks over a 20×10-cell slot |

Playlist shuffle sends `play` with the playlist URI, waits for acceptance and confirmation of that playlist on the current Soloist session, then sends `set_shuffle`. An intervening observed change away from the confirmed context cancels the pending action. The commands are not atomic: an unobserved context switch between confirmation and command execution can still affect different playback. The first track is not guaranteed to be random. Spotify manages Autoplay; Pulso does not modify or simulate it.

Save keeps Soloist's `item.uri` only when it is a track URI, so a missing or non-track item leaves the action unavailable instead of saving the wrong thing. Both write paths use Spotify's current playlist-items route: adding keeps the `uris` body and removing sends `items` with the playlist's `snapshot_id`. A `403` that does not clearly state that this account cannot change that particular playlist offers one explicit reauthorization with the wizard's browser-and-link presentation and retries once; a refusal that names the playlist the account cannot edit is reported as such without a retry. Playback is never touched and Spotify response bodies are never rendered.

## Out of this slice

Albums, artists, liked songs and podcasts need new scopes and a new authorization, so they stay out. There is no device selection either: Pulso always plays in this console, which is why the box spends no column on a device and only names a remote one in its context line. No endpoint, payload or scope was added for the cover beyond the single narrow track query.

## Constraints and risks

- Playlist contents stay limited to owned or collaborative playlists in Spotify development mode; the fallback is part of the design, not an error.
- Saving and removing a track depend on the Web API write scopes: an older read-only session is recovered with one explicit reauthorization, and a rejection that does not clearly name a playlist the account cannot edit offers that same single reauthorization instead of claiming the playlist cannot be changed.
- Removing a track needs the snapshot of the playlist the row came from; a failed or missing snapshot leaves removal unavailable, a stale snapshot makes Pulso read the listing again and ask again, and duplicates are removed one at a time.
- Search is capped at 10 results per request; the table pages instead of truncating silently.
- Seek and mode commands are best-effort: Soloist confirms dispatch, not the resulting state; the box shows the reported event.
- Text entry swallows `q`, `s`, `r`, `+` and `-` so a search query can contain them.
- Unicode glyph width is fixed at one cell; no emoji, and every state is a glyph plus a word, never colour alone.
- The transport, the setup wizard, pause-on-exit, preferences and the theme system are unchanged; the workspace only consumes them.
