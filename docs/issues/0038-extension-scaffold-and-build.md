---
status: open
title: Scaffold Chrome MV3 extension with Vite build
milestone: M12
---

Create `extension/` with an MV3 manifest, Vite build, Vitest setup, and a tsconfig
path alias resolving the client source and generated protobuf types. Add
`make extension`, `make extension-dev` and `make extension-test`, reusing the client
container with `-w /app/extension`, and add `extension-test` to `make test`.

Popup shell with text placeholders for the controls that arrive in later issues.
