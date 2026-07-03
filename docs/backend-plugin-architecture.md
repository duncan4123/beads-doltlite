# Backend Plugin Architecture Sketch

This branch starts the backend plugin seam without changing user-facing backend
behavior.

The first hook point is the configured store open path:

```text
metadata.json -> backend provider lookup -> provider.Open -> storage.DoltStorage
```

Dolt remains the default backend. Future plugin-shaped backends should enter
through the same provider/process seam instead of adding backend-specific
branches to command code.

## Current Scope

- `internal/backend` defines provider registration, capabilities, and open
  options.
- Built-in providers register `dolt`.
- `cmd/bd/store_factory.go` routes configured backend opens through the
  provider registry.
- `metadata.json` preserves non-legacy backend names so plugins can be looked
  up by name; only empty and old `sqlite` values are normalized to `dolt`.
- `backend/plugin` exposes the v1alpha1 process protocol and type aliases
  plugin authors need without importing Beads `internal/types`.
- `internal/backend/pluginprocess` can launch an external backend process,
  open a session over the v1alpha1 newline-delimited JSON protocol, and expose
  the first storage methods needed by basic issue/config/ready-work flows.
- Existing direct `dolt.Config` construction is intentionally left alone for
  now because bootstrap/server/proxy paths carry extra behavior that should be
  split into later reviewable PRs.

## Plugin Implications

The registry remains the in-process seam for Beads' built-in Dolt backend. The
external process path lets maintainers evaluate plugin boundaries without
shipping backend-specific code in core. DoltLite demonstrates that shape in an
out-of-tree plugin:

```text
https://github.com/duncan4123/beads-backend-doltlite
```

Out-of-tree binaries can be tested by setting `backend_plugin_command` and,
optionally, `backend_plugin_args` in `.beads/metadata.json`. The client defaults
to passing `serve` when no args are configured, matching the DoltLite plugin.
