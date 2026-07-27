---
status: open
title: Extension sync orchestration and run log
milestone: M12
dependencies: [0036, 0039, 0040]
---

Wire the Sync button: resolve the window, run the recipe, convert, upload with the
requested window as the period, and poll the job to a terminal state.

Warn prominently when rows were dropped and record the distinct unrecognised
transaction types. Refuse to upload a zero-transaction result, since the server
short-circuits an empty replace and nothing would be deleted. Run log retaining the
window derivation, recipe steps, dropped rows, job id and outcome.
