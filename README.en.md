# Pulso

English · [Español (predeterminado)](README.md)

Pulso is a Spotify player for the terminal written in Go on top of **Spotify Soloist**, Spotify's official player, plus the **Spotify Web API** for searching and browsing your playlists. I built it for my own use because `ncspot` and `spotify_player` depend on `librespot` and stopped playing audio on my account after Spotify changed its audio keys.

It is not trying to be the best Spotify client or to compete with other projects. Its scope is what I actually use: open a terminal, search for a song, pick a playlist and listen without keeping the official app open. As long as it works for me, it is good enough.

## Main features

- Playback through Spotify's official engine (Soloist), without `librespot`.
- One-time guided pairing, then automatic detection when Spotify connects the device.
- Playback on launch: pause, next, previous, volume, seek, shuffle and repeat.
- Library with your playlists and a song table with artist and duration.
- Track search from the top bar.
- Terminal workspace with panels, focus-aware shortcuts and a help screen.
- English and Spanish, and standard, dark and light themes.
- Soloist installation and updates from the app itself, without administrator rights.
- A privately stored Spotify session: no repeated Client ID or browser approval.

## Platforms

Windows x86-64 and Linux x86-64 are the intended platforms. macOS builds, but I have not tested it. Spotify Soloist ships for Linux (x86-64, arm64, arm32), Apple Silicon macOS and Windows x86-64: Pulso cannot play audio where Spotify has no Soloist build.

## Install and use

Requires **Go 1.22 or newer** to build and a **Spotify Premium** account.

```sh
go install ./cmd/pulso
pulso
```

On first launch the wizard checks whether Soloist is installed, offers to download it from Spotify, guides you through creating the Soloist key and pairs the device from the Spotify app on your phone. It then asks once for the Client ID of your own Spotify app to search and list playlists.

Read the [full instructions](INSTRUCTIONS.en.md) for pairing, the Spotify app, shortcuts, themes and troubleshooting.

## Status

It works on my Windows machine: playback, library, search, languages, themes and the stored session. Not yet verified live: native Linux, Spotify's real refresh-token rotation and how the layout looks in other terminals.

Spotify's own limits apply too: in development mode only playlists you own or collaborate on expose their tracks, and search returns ten results per page.
