# TUI reformulation

Redesign of the player surface, inspired by the pane-and-focus layout of spotatui, while keeping Pulso's Soloist playback and Spotify Web API use. Playback, saving, removing and pairing behavior must not change.

## Specs

- S1 — “quiero una reformulacion de la ui https://github.com/LargeModGames/spotatui quiero que hagas una tui muestrame la propuesta en formato tui”
- S2 — “hay que quitar los comandos de abajo, tambien hay que quitar el tab para pasar de foco sino que cada parte tendra un comando especifico el reproductor estara abajo donde se mostrara de estaforma sera una caja donde en la parte superior de la caja dentro de la liena de la caja dira si esta en pausa o reprodudiendo si esta en aleatorio si esta en repeticion y el volumen el reproductor tendra la imagen de la cancion a la izquierda al lado el nombre de la cacion abajo el disco/ nombre artistita y abajo el tiempo de reproduccion con el la barra de tiempo de reproduccion”
- S3 — “quiero que generes el plan”

## Tasks

- T1 [S2] Bottom player box: state line embedded in the box's top border (play/pause, shuffle, repeat, volume), album cover on the left, title, album/artist, and elapsed time with a time bar; degrade to a solid block with initials when the cover or color is unavailable. Own commit.
- T2 [S2] Three-zone layout: library rail, main table, and a right column with what's next plus the context; the now-playing facts appear only inside the player box. Responsive at 118 and 80 columns. Own commit.
- T3 [S2] Remove the footer hint line and `Tab`/`Shift+Tab`; give each zone its own command (`l` library, `/` search, `c` queue, `Esc` back to the table) while player keys stay global; keep `?` as the only key reference. Own commit.
- T4 [S1] Documentation and regression coverage: help screen, `INSTRUCTIONS.md`/`INSTRUCTIONS.en.md`, `docs/UI-PLAN.md`, width and overflow tests at 118×32 and 80×24, and the tests that pin the previous keys and commands. Own commit.
- T5 [S1–S2] Visual verification of the shipped sizes with a captured terminal, comparing wide and narrow before/after, plus `go test ./...` and the known pre-existing Windows failures recorded.

## Log

- L1 — User: “quiero una reformulacion de la ui https://github.com/LargeModGames/spotatui quiero que hagas una tui muestrame la propuesta en formato tui”
- L2 — Proposal shown as terminal mockups: three zones with focus, upper status line and a transport footer; taken from spotatui the pane focus, density and control bar, not its sources, visualizer or plugins.
- L3 — User: “hay que quitar los comandos de abajo, tambien hay que quitar el tab para pasar de foco sino que cada parte tendra un comando especifico el reproductor estara abajo …”. Revised proposal: no hint bar, no `Tab`, per-zone keys, and the player as a bottom box whose top border carries play state, shuffle, repeat and volume, with cover, title, album/artist and the time bar inside.
- L4 — User: “quiero que generes el plan”. Delivery is split into player, layout, command model, docs/tests and visual verification. No implementation started and no branch created yet.
