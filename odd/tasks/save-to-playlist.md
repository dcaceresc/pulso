# Save track to playlist

Part 2 of the playback plan. Part 1 (playlist shuffle) shipped on `feat/playlist-shuffle`; this branch stacks on it. Playback must keep working when saving fails or is unavailable.

## Specs

- S1 — “si el usuario hace aletorio sobre una cancion sea aletorio contra todo como esta actualmente”
- S2 — “el modo aletorio normal donde hace aletorio con la lista y si se acaban las canciones pone canciones aleatorios que tengan que ver con la lista”
- S3 — “implementa la parte 1”
- S4 — “lo de guardar quiero guardar la cancion en la lista”
- S5 — “ahora continua con la parte 2”
- S6 — “modifica el plan original y la rama para que pueda agregar o quitar canciones de una lista”
- S7 — “no funciona guardar da el siguiente error No se pudo guardar en Power Metal: esta cuenta no puede cambiar esa lista. esa playlist la cree yo”

## Tasks

- T1 [S1–S3] Playlist shuffle mode, verified and natively approved; commits `30cac40`, `b543aac`, `08eb63f`.
- T2 [S2] Verify exhaustion and Autoplay with a short real Spotify playlist; blocked on a designated playlist and permission to change playback, or the user's live result.
- T3 [S4–S5] Spotify write layer: request playlist write scopes, add the current track to a chosen playlist, recover from a missing write grant with one explicit re-authorization, and never surface response bodies; commit `f80e28d`.
- T4 [S4–S5] Player action to save the playing track into a chosen playlist, with confirmation only after Spotify accepts and a non-blocking failure notice; help, docs and tests; commits `570cdeb` and `960ed77`.
- T5 [S4] Live acceptance: save a real track and confirm it in the chosen Spotify playlist, not only in the queue; blocked on a live Spotify write.
- T6 [S6] Spotify remove layer: delete one occurrence of a chosen track from a playlist using the snapshot the listing came from, with typed failures and no response bodies; commit `bb5e507`.
- T7 [S6] Player action to remove the highlighted track from the playlist being browsed, with explicit confirmation, a localized result notice and untouched playback; commit `3679630`.
- T8 [S6] Live acceptance: remove a real track and confirm it disappeared from the chosen playlist while other entries stayed; blocked on a live Spotify write.
- T9 [S7] Saving a track to your own playlist reports that the account cannot change it: the write paths must use Spotify's current documented playlist-items endpoint and bodies, and a refusal must distinguish a missing write grant from a list the account really cannot edit; commit pending.

## Log

- L1 — User: “implementa la parte 1”
- L2 — Referenced Part 1: keep existing `Enter` and `s` on standalone tracks; add an explicit shuffle action targeting the playlist URI; differentiate the two modes in Pulso. Let Spotify Autoplay supply similar music after exhaustion if enabled. Do not simulate it or claim live behavior without evidence. Do not add playlist writes.
- L3 — Branch `feat/playlist-shuffle` based on merged `master`. Preserve the existing local edit to `AGENTS.md` outside this delivery.
- L4 — Review slice `0416ed5..30cac40` (551 authored diff lines) keeps the session-bound command, UI, protocol tests, and docs together as one testable behavior. Nothing published. Writer observed RED and focused GREEN; pending independent verification and native review.
- L5 — `go run ./cmd/pulso doctor` passed: Soloist was signed in and was the active device. T2 needs a short test playlist and permission to change active playback, or the user's real-playback result. No live playback was sent.
- L6 — Independent verifier reproduced a standalone-play regression in `30cac40`: an untagged error dropped the socket on the candidate, but ordinary commands still worked on the base. `b543aac` preserves ordinary playback/error handling and quarantines uncertain ACKs only when a later playlist-shuffle action needs a fresh session. Writer confirmed RED then GREEN on the regression. Final review slice `0416ed5..b543aac` (627 authored diff lines), pending independent recheck and native review.
- L7 — Independent recheck at `b543aac` passed S1–S3 for T1, including 11 identical legacy command traces. Full suite retained only the two Windows uninstall failures reproduced on base.
- L8 — Native review `review-2448f5d382033059` identified R3-001; user authorized the bounded correction. `08eb63f` cancels the obsolete ACK timer when ordinary playback supersedes the playlist intent, quarantines ambiguous ACKs and verifies the same session beyond the deadline (RED/GREEN). The correction changed 20 diff lines against an 85-line plan. Final source slice `0416ed5..08eb63f` (641 authored diff lines), pending native validation.
- L9 — Targeted validator accepted `08eb63f`; native review `review-2448f5d382033059` approved and closed with acknowledged authority. T1 complete. T2 remains pending live evidence. No push or PR.
- L10 — User: “ahora continua con la parte 2”
- L11 — Accepted Part 2 plan: add “Guardar canción en…” for the playing track, choose a playlist, request write permission only for this action, show success only after Spotify confirms, and report failure without interrupting playback. Branch `feat/save-to-playlist` stacks on `feat/playlist-shuffle` because Part 1 is not merged. Keep `AGENTS.md` outside the candidate.
- L12 — Part 2 commits: `f80e28d` write layer (scopes, POST `/tracks`, typed 403s, explicit reauthorization) and `570cdeb` player action (`a`, chooser over the library playlists, target named before Enter, one retry after reauthorization, localized non-blocking notices) plus docs. Soloist `item.uri` now feeds `Track.URI` for track URIs only. Part 2 slice `08eb63f..570cdeb`, larger than the advisory 400 lines because tests, docs and the panel state machine ship with the behavior. Writer RED/GREEN observed; local builds and touched packages green; independent verification and native review pending. No publishing.
- L13 — Independent verification found no real defects (18-scenario key/command differential identical, POST boundary exact, failures quiet, one retry); advisory notes concern footer reflow and hint labels. Full suite kept only the two pre-existing Windows uninstall failures.
- L14 — Native review `review-b740bddcf1126bf0` required six corrections: preserve the confirmed track and target across the approval retry, let later Soloist notices show again, and adopt the reauthorized catalog as the single session owner. `960ed77` fixes them in 139 diff lines against a 140-line plan (131 additions, 8 deletions) with focused RED/GREEN. Targeted validator accepted; native review approved and closed, authority acknowledged. T5 remains blocked on a live Spotify save. No push or PR.
- L15 — User: “modifica el plan original y la rama para que pueda agregar o quitar canciones de una lista”. Part 2 now also removes one occurrence of a track from a playlist, using the snapshot of the listing it was chosen from and an explicit confirmation; playback stays untouched. The plan documents stop claiming playlist edits are out of scope.
- L16 — Removal commits: `bb5e507` Spotify layer (snapshot-carrying read, DELETE with the row's snapshot, typed conflict/scope errors) and `3679630` player action (`x` on a browsed track page, prompt naming track and playlist, stale snapshot re-reads before asking again, one reauthorization retry, quiet failures, reload after success) plus docs. Independent verification passed T6–T7, with a 31-step differential identical except the new `x` hint, and only the two pre-existing Windows uninstall failures in `./...`. Part 2 slice is `08eb63f..3679630`; removal alone is `960ed77..3679630` (1000 additions, 97 deletions). T5/T8 remain blocked on live Spotify writes. Native review pending.
- L17 — Native review `review-a39ffdd44d87a459` stopped with `native_stop_required` (horizon terminal, state escalated, cause unknown_causality for R1-001; refuter corroborated R3-001, R3-002, R4-001 and R4-002). No continuation was offered. The user chose to leave the branch pending without disabling review, so nothing is published and no further correction is attempted on this candidate.
- L18 — User: “no funciona guardar da el siguiente error No se pudo guardar en Power Metal: esta cuenta no puede cambiar esa lista. esa playlist la cree yo”. The reported refusal is classified as not editable even though the person owns the playlist, and the write paths still use the deprecated `/tracks` route; the documented current route for both add and remove is `/v1/playlists/{id}/items` (add body `uris`, remove body `items` plus snapshot). T9 fixes the routes and the refusal mapping, then needs a live retry.
