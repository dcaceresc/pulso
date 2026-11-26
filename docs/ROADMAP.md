# Pulso roadmap: inspired by Spotatui

Keep Pulso a keyboard-first Spotify terminal app for Windows and Linux. Use official Soloist playback and the official Spotify Web API; the roadmap is not a promise to clone every Spotatui feature.

## What Spotatui documents

| Area | Features | Pulso phase |
| --- | --- | --- |
| Spotify | Search, album and artist navigation, library and playlists, queue, Connect devices, native streaming or Soloist | 1–2; do not add a librespot dependency |
| Interface | Terminal and desktop clients, saved configuration, configurable keybindings, colors and themes, artwork | 1–3; desktop is separate |
| Listening | Synced lyrics, live visualizer, audio quality badge, playback controls, local history, statistics and shareable recap | 2–4 |
| CLI and integrations | Shell commands, Discord presence, Lua plugins, AI DJ, MCP and ChatGPT integrations | 3–5; opt-in only |
| Social and multiple sources | Friends, listening parties, mixed-source queue, playlist mirroring, local files, Subsonic/Navidrome, radio, YouTube, Qobuz and Apple Music on macOS; Spotify is also a source | 5; separate evaluation |
| Distribution | Prebuilt packages, feature-specific builds and self-updates | 3; keep `go install` as the main Pulso install path |

## Delivery order

1. **Catalog and personalization.** Add track search, playlist listing and playable results with OAuth PKCE. Keep Now Playing functional when authorization is missing. Ship English and Spanish and selectable standard, dark and light themes. Verify both languages and all palettes in wide and compact terminals.
2. **Daily controls.** Add track filtering where playlist contents are available, paste-and-play links, add-to-queue, seek, shuffle/repeat, and a richer album/artist view. Consider playlist edits only after a separate scope and permission review, and only where the official API allows it.
3. **Keyboard and polish.** Add configurable shortcuts, help, cover art with text-only fallback, local CLI commands and safe Soloist update guidance. Verify behavior on Windows and Linux before tagging a release.
4. **Listening enhancements.** Add opt-in local history and statistics, then evaluate synced lyrics from a lawful documented provider. Verify audio-quality metadata before exposing a badge; a true visualizer needs a supported audio signal, not a fabricated animation. Provide clear local-data controls.
5. **Extensions and social.** Assess plugins, Discord presence, AI/MCP, shared playback, a desktop frontend, and extra sources as independent projects. Evaluate authorization, privacy and maintenance before adding each; do not let them block the Spotify experience.

## Spotify constraints

The Spotify Web API and Soloist use different authorization. In development mode Spotify can withhold playlist items when the user is neither the owner nor a collaborator; offer playback by playlist URI instead of reporting an empty playlist. Do not reconstruct restricted catalog data through undocumented endpoints. Soloist remains responsible for audio and the one-time Spotify Connect pairing.

Sources: [Spotatui README](https://github.com/LargeModGames/spotatui), [feature index](https://github.com/LargeModGames/spotatui/blob/main/docs/README.md), [themes](https://github.com/LargeModGames/spotatui/blob/main/docs/themes.md), [Soloist integration](https://github.com/LargeModGames/spotatui/blob/main/docs/soloist.md), and [Spotify development-mode guide](https://developer.spotify.com/documentation/web-api/tutorials/february-2026-migration-guide).
