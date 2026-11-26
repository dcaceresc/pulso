# Pulso

Pulso is a Go terminal app in development for using Spotify with the official [Soloist](https://developer.spotify.com/documentation/soloist) playback engine. See [the plan](PLAN.md) for the Windows and Linux MVP.

## Try the local diagnostic

From this directory with Go 1.22 or newer:

```sh
go run . doctor
```

`doctor` looks for Soloist on `PATH` and then in the `pulso/bin` subdirectory of your user configuration directory. It reports Soloist's own daemon, WebSocket, and sign-in status. For an existing Soloist instance with a custom data directory, use `go run . doctor --data-dir PATH`.

The diagnostic does not install or start Soloist. Use [Spotify's official downloads](https://developer.spotify.com/documentation/soloist/reference/downloads-and-updates) if you want to try it with a real installation. The interactive interface and assisted setup are not implemented yet.

Install a development checkout with `go install .`. The versioned `go install` path will be available once the project has a published module path.
