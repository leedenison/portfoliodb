---
status: open
title: Data-driven broker recipe model and Fidelity UK recipe
milestone: M12
dependencies: [0038]
---

Recipe schema and a generic interpreter that contains no broker knowledge, so
repairing a broken site integration is an edit to recipe data. Selectors are
ordered candidate lists; each step logs which candidates were tried and which
matched. Request replay is the preferred capture strategy, with DOM driving as a
fallback.

Add the Fidelity UK recipe and jsdom fixture tests built from a captured page.
Include the popup dry-run mode that runs through capture and conversion without
uploading, since that is how a recipe is developed against the live site.
