# Backend Plugin Architecture Sketch

This branch starts the backend plugin seam without changing user-facing backend
behavior.

The first hook point is the configured store open path:

```text
metadata.json backend name -> trusted local plugin config -> provider.Open -> storage.DoltStorage
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
  It does not authorize an executable.
- Trusted plugin commands resolve from local-only sources: the
  `BEADS_BACKEND_PLUGIN_COMMAND` environment variable, `.beads/config.local.yaml`,
  or user-global config. This keeps clone-time metadata from becoming code
  execution when hooks run `bd` automatically.
- `backend/plugin` exposes the v1alpha1 process protocol and type aliases
  plugin authors need without importing Beads `internal/types`.
- `internal/backend/pluginprocess` can launch an external backend process,
  open a read/write or read-only session over the v1alpha1 newline-delimited JSON protocol, and expose
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

Out-of-tree binaries can be installed with:

```bash
bd backend install doltlite --command /path/to/bd-backend-doltlite
```

That command records `backend = "doltlite"` in committed `.beads/metadata.json`
and writes the executable trust record to `.beads/config.local.yaml`. The local
file is gitignored by the canonical `.beads/.gitignore`; cloning a repository
with hostile `backend_plugin_command` metadata is therefore not sufficient to
make `bd` execute it.
