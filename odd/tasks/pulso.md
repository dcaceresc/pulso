# Pulso implementation
Product scope: [PLAN.md](../../PLAN.md). Start with a local, read-only Soloist diagnostic.
Target: Windows x86-64 and Linux x86-64.

## Specs

- S1: “quiero construir una tui pero en go genera un plan”
- S2: “necesito que tambien funcione en linux”
- S3: “quiero que se instale mediante go install asi sirve para cualquier, define el nombre”
- S4: “no me gusta el nombre da mas opciones ademas quiero que cuando lo instale verifique si esta instalado o funcionado soloist y trate de instalarlo”
- S5: “me gusta pulso”
- S6: “quiero una interfaz grafica que de la bienvenida y verifique si esta instalado soloist sino te lo instale y te de los pasos para crear la key y poder agregarla”
- S7 (selected): “Dentro de la terminal (TUI)”

- S8: “no funciona ademas se queda pegado aqui Welcome to Pulso

Could not install Soloist. Try again?

Y: retry install | Esc: quit ademas quiero una interfaz grafica como la de spotify-tui o la de sqlterm que esta en la carpeta personal tienes permiso para leerla”

- S9: “hay que dejar esta informacion para el usuario para que entienda que hay que hacer, ya lo hice y dice reproduciendo en pulso ahora falta toda la intefaz de visualizacion”
- S10: “.\player_transport.go:185:6: other declaration of playerText  Soloist reported a playback error.”

## Tasks

- T1 [done] S1–S5 — Bootstrap the Go command and read-only Soloist diagnostic; inline; commit: `feat: add Pulso Soloist diagnostic`.
- T2 [blocked] S2, S4 — Verify Soloist playback and Web API access on both target platforms; waits for a paired Soloist and authorized Spotify developer app on the target environments.
- T3 [done] S2, S4, S6 — Managed Soloist installer for missing Windows/Linux amd64 builds; worker; commit `2502892`; independent fixture checks passed.
- T7 [done] S6, S7 — Bubble Tea welcome, setup status, installation confirmation, and masked key entry; worker; commit `dac65d9`; independent model checks passed.
- T8 [done for local fixtures] S6, S7 — Default launch and pairing in `4434863`; follow-up commit `fix: guide pairing for existing Soloist devices`. Real Spotify pairing stays in T2.
- T4 [pending independent verification] S1, S2, S9 — Playback transport and Now Playing UI with controls, upcoming queue, and reconnect; commits `feat: stream Soloist playback events` and `feat: open the Now Playing workspace`.
- T11 [pending independent verification] S9 — Pairing instructions in `ddb56eb`; ready-session handoff in `feat: open the Now Playing workspace`.
- T5 [pending] S1, S2 — Add Web API search and playlist browsing; route pending; commit pending.
- T6 [pending] S2, S3, S5 — Verify versioned `go install` and real end-to-end behavior on both platforms; route pending; commit pending.

- T9 [done] S8 — Accept known release documentation without extracting it; commit `2087a24`. The independent verifier passed 32 subtests on the committed snapshot.
- T10 [done for automated checks] S8 — Responsive full-screen setup and install recovery; commit `feat: redesign the Soloist setup workspace`. Render bounds and recovery tested; no visual capture.

## Log

- L1: “comienza con la implementacion”
- L2: The workspace contains the plan but no Git repository or `soloist` on the current shell's PATH. Start with a local diagnostic without downloads or account access. Use a placeholder module path until a repository owner is known.
- L3: The focused tests initially failed to compile before the command existed, then passed. The full suite, `go vet`, local Windows `go install .`, Windows help and missing-Soloist smoke checks, and an amd64 Linux cross-build passed. No real playback, Web API, or native Linux run has been verified. Native assessment did not return a tier (`package-local-binary-missing`).
- L4: “quiero una interfaz grafica que de la bienvenida y verifique si esta instalado soloist sino te lo instale y te de los pasos para crear la key y poder agregarla”
- L5: Selected “Dentro de la terminal (TUI)”. Fake-backed implementation can proceed while real account/platform validation in T2 remains blocked.
- L6: Setup visual brief: one compact pane with a Pulso welcome, a clear status or next action, and one row of keyboard hints. Let prose fit an 80-column terminal. Key entry must be masked and not written to disk by Pulso in this slice. Use the key for a local Soloist launch; Spotify owns the persistent pairing. A running external instance is not restarted or replaced by the wizard.
- L7: Installer and model fixture checks passed. The independent verifier could only inspect committed code; it identified a zero-CRC ZIP gap by static inspection. The parent reproduced that bug with a fixture, fixed it, and confirmed the installer and model suite passed. The independent verifier then ran successfully on the corrected committed snapshot `dac65d9` (7 tests and 29 subtests including independent scratch probes). Live CDN, real pairing and native Linux remain unverified.
- L8: T8 Windows PTY checks confirmed the welcome, Esc, and pairing recheck. The independent check found misleading pairing copy for external devices; the parent observed RED, updated the copy, and ran the expanded service/model/CLI checks and full Go suite to GREEN. `go vet`, local Windows install and Linux amd64 cross-build passed. Native assessment unavailable (`package-local-binary-missing`).
- L9: Downloaded the official ZIP and tar.gz (both HTTP 200) without executing them. Both include CHANGELOG.md and THIRD_PARTY_LICENSES.txt. The actual ZIP reproduced `unexpected ZIP file "CHANGELOG.md"`. After the narrow fix, both real archives passed extraction validation. sqlterm is a read-only layout reference: header, panel/sidebar, separate status and shortcut rows, responsive collapse.
- L10: Ran the corrected installer against the official CDN on Windows in an isolated temporary directory. It installed only `soloist.exe` (22,562,104 bytes); the executable was not started and no account was used.
- L11: Responsive renders and install-failure tests passed, plus the full Go suite, vet, Linux amd64 cross-build and diff check. No graphical screenshot or interactive proof of this new layout. Native assessment unavailable (`package-local-binary-missing`).
- L12: “hay que dejar esta informacion para el usuario para que entienda que hay que hacer, ya lo hice y dice reproduciendo en pulso ahora falta toda la intefaz de visualizacion”. The user reports a real paired device playing audio; Web API and both-platform verification remain separate.
- L13: Pairing instructions added to the wizard and README in `ddb56eb`; focused RED and GREEN observed. Setup-to-player model handoff passed its focused tests in isolation. Production now opens the player for a previously paired session or after Enter confirms new pairing. Focused tests, full suite, vet and Linux amd64 cross-build passed. High-risk concurrency assessment unavailable (`package-local-binary-missing`); independent committed checks pending.
- L14: “.\player_transport.go:185:6: other declaration of playerText  Soloist reported a playback error.” The committed tree has one `playerText` declaration and the initial build passed; the duplicate was not reproduced. Comparing the Soloist WebSocket contract with transport requests found the playback break: requests used the command name as `type` instead of `type: command` plus `command: <name>`. Contract-matching fixtures reproduced the malformed payload before the fix.
