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

## Tasks

- T1 [done] S1–S5 — Bootstrap the Go command and read-only Soloist diagnostic; inline; commit: `feat: add Pulso Soloist diagnostic`.
- T2 [blocked] S2, S4 — Verify Soloist playback and Web API access on both target platforms; waits for a paired Soloist and authorized Spotify developer app on the target environments.
- T3 [pending independent verification] S2, S4, S6 — Managed Soloist installer for missing Windows/Linux amd64 builds; worker; candidate `feat: add managed Soloist installer`.
- T7 [pending independent verification] S6, S7 — Bubble Tea welcome, setup status, installation confirmation, and masked key entry; worker; candidate `feat: add Pulso welcome wizard`.
- T8 [in progress] S6, S7 — Wire the default command to setup, launch Soloist locally, guide and confirm pairing, and preserve `doctor`; worker; commit pending.
- T4 [pending] S1, S2 — Build the TUI player, playback controls, queue, and reconnect behavior; route pending; commit pending.
- T5 [pending] S1, S2 — Add Web API search and playlist browsing; route pending; commit pending.
- T6 [pending] S2, S3, S5 — Verify versioned `go install` and real end-to-end behavior on both platforms; route pending; commit pending.

## Log

- L1: “comienza con la implementacion”
- L2: The workspace contains the plan but no Git repository or `soloist` on the current shell's PATH. Start with a local diagnostic without downloads or account access. Use a placeholder module path until a repository owner is known.
- L3: The focused tests initially failed to compile before the command existed, then passed. The full suite, `go vet`, local Windows `go install .`, Windows help and missing-Soloist smoke checks, and an amd64 Linux cross-build passed. No real playback, Web API, or native Linux run has been verified. Native assessment did not return a tier (`package-local-binary-missing`).
- L4: “quiero una interfaz grafica que de la bienvenida y verifique si esta instalado soloist sino te lo instale y te de los pasos para crear la key y poder agregarla”
- L5: Selected “Dentro de la terminal (TUI)”. Fake-backed implementation can proceed while real account/platform validation in T2 remains blocked.
- L6: Setup visual brief: one compact pane with a Pulso welcome, a clear status or next action, and one row of keyboard hints. Let prose fit an 80-column terminal. Key entry must be masked and not written to disk by Pulso in this slice. Use the key for a local Soloist launch; Spotify owns the persistent pairing. A running external instance is not restarted or replaced by the wizard.
- L7: Installer and model fixture checks passed. The independent verifier could only inspect committed code; it identified a zero-CRC ZIP gap by static inspection. The parent reproduced that bug with a fixture, fixed it, and confirmed the installer and model suite passed. Candidate verification must not use the old baseline.
