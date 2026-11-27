# Playlist shuffle

Scope: playback only. Preserve the existing standalone track path and leave playlist writes out of this work.

## Specs

- S1 — “si el usuario hace aletorio sobre una cancion sea aletorio contra todo como esta actualmente”
- S2 — “el modo aletorio normal donde hace aletorio con la lista y si se acaban las canciones pone canciones aleatorios que tengan que ver con la lista”
- S3 — “implementa la parte 1”

## Tasks

- T1 [S1–S3] Implement the explicit playlist-shuffle action, keep standalone playback unchanged, and document Spotify-managed Autoplay; completed; independently verified and native review approved; playback commits `30cac40`, `b543aac` and `08eb63f`.
- T2 [S2] Verify exhaustion and Autoplay with a short real Spotify playlist, empty manual queue and repeat disabled; blocked on a designated test playlist and permission to change playback, or the user's live test result; same candidate, no additional commit planned.

## Log

- L1 — User: “implementa la parte 1”
- L2 — Referenced Part 1: keep existing `Enter` and `s` on standalone tracks; add an explicit shuffle action targeting the playlist URI; differentiate the two modes in Pulso. Let Spotify Autoplay supply similar music after exhaustion if enabled. Do not simulate it or claim live behavior without evidence. Do not add playlist writes.
- L3 — Branch `feat/playlist-shuffle` based on merged `master` (`0416ed5`). Preserve the existing local edit to `AGENTS.md` outside this delivery.
- L4 — Review slice `0416ed5..30cac40` (551 authored diff lines) keeps the session-bound command, UI, protocol tests, and docs together as one testable behavior. Nothing published. Writer observed RED and focused GREEN; pending independent verification and native review.
- L5 — `go run ./cmd/pulso doctor` passed: Soloist was signed in and was the active device. T2 needs a short test playlist and permission to change active playback, or the user's real-playback result. No live playback was sent.
- L6 — Independent verifier reproduced a standalone-play regression in `30cac40`: an untagged error dropped the socket on the candidate, but ordinary commands still worked on the base. `b543aac` preserves ordinary playback/error handling and quarantines uncertain ACKs only when a later playlist-shuffle action needs a fresh session. Writer confirmed RED then GREEN on the regression. Final review slice `0416ed5..b543aac` (627 authored diff lines), pending independent recheck and native review.
- L7 — Independent recheck at `b543aac` passed S1–S3 for T1, including 11 identical legacy command traces. Full suite retained only the two Windows uninstall failures reproduced on base.
- L8 — Native review `review-2448f5d382033059` identified R3-001; user authorized the bounded correction. `08eb63f` cancels the obsolete ACK timer when ordinary playback supersedes the playlist intent, quarantines ambiguous ACKs and verifies the same session beyond the deadline (RED/GREEN). The correction changed 20 diff lines against an 85-line plan. Final source slice `0416ed5..08eb63f` (641 authored diff lines).
- L9 — Targeted validator accepted `08eb63f`; native review `review-2448f5d382033059` approved and closed with acknowledged authority. T1 complete. T2 remains pending live evidence. No push or PR.
