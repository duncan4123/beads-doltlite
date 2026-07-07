#!/usr/bin/env bash
#
# Deep conformance gate: the bts-rs differential oracle.
# Reference = bd in Dolt mode; candidate = the SAME bd routed through a backend
# wrapper. The wrapper adapts the backend-agnostic harness to backend-specific
# init/install plumbing.
#
# Defaults remain compatible with the original Postgres gate:
#   BEADS_DEEP_ORACLE_BACKEND=postgres ./scripts/run-oracle-p.sh
#
# DoltLite plugin gate:
#   BEADS_DEEP_ORACLE_BACKEND=doltlite-plugin \
#   BEADS_DOLTLITE_PLUGIN_COMMAND=/path/to/bd-backend-doltlite \
#   ./scripts/run-oracle-p.sh
#
# Env:
#   BEADS_DEEP_ORACLE_BACKEND postgres|doltlite-plugin              (default: postgres)
#   BTS_RS_DIR                 external bts-rs checkout              (optional)
#                              unset uses tests/oracle-a/harness
#   BTS_CATALOG_FILE           enumerated catalog JSON for vendored harness
#                                                                 (optional)
#   BEADS_DEEP_ORACLE_REQUIRE_CATALOG fail if the 500+ catalog is unavailable
#                                                                 (optional)
#   BTS_ONLY                   scenario name filter passed through   (optional)
#   BD                         path to a prebuilt bd binary          (optional; built if unset)
#
# Postgres env:
#   BEADS_PG_TEST_URL          postgres URL incl. password           (required for postgres)
#   BEADS_POSTGRES_PLUGIN_COMMAND backend plugin command             (optional)
#   BEADS_POSTGRES_PLUGIN_ARGS    backend plugin args                (optional; default: serve)
#
# DoltLite plugin env:
#   BEADS_DOLTLITE_PLUGIN_COMMAND backend plugin command             (required)
#   BEADS_DOLTLITE_PLUGIN_ARGS    backend plugin args                (optional; default: serve)
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

die() {
  printf 'run-oracle-p: %s\n' "$*" >&2
  exit 2
}

shell_quote() {
  local escaped
  escaped="$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
  printf "'%s'" "$escaped"
}

BACKEND="${BEADS_DEEP_ORACLE_BACKEND:-postgres}"
BUILD_TAGS="${BUILD_TAGS:-gms_pure_go}"
SCRATCH="$(mktemp -d)"
BD_BUILT=0
BD="${BD:-}"

cleanup() {
  local rc=$?
  rm -rf "$SCRATCH"
  if [[ -n "${BTS_RS_DIR:-}" ]]; then
    git -C "$BTS_RS_DIR" checkout crates/bts-conformance/testdata/golden >/dev/null 2>&1 || true
  fi
  return "$rc"
}
trap cleanup EXIT

command -v go >/dev/null 2>&1 || die "go not found"
command -v cargo >/dev/null 2>&1 || die "cargo not found (Rust toolchain required)"

if [[ -z "$BD" ]]; then
  BD="$SCRATCH/bd"
  BD_BUILT=1
  echo "### building bd -> $BD"
  CGO_ENABLED=1 go build -tags "$BUILD_TAGS" -o "$BD" ./cmd/bd
fi
[[ -x "$BD" ]] || die "bd binary is not executable: $BD"

WRAP="$SCRATCH/bd-candidate-wrapper"
CANDIDATE_LABEL=""

case "$BACKEND" in
  postgres)
    : "${BEADS_PG_TEST_URL:?set BEADS_PG_TEST_URL (postgres URL incl. password)}"
    PLUGIN_COMMAND="${BEADS_POSTGRES_PLUGIN_COMMAND:-}"
    PLUGIN_ARGS="${BEADS_POSTGRES_PLUGIN_ARGS:-serve}"
    CANDIDATE_LABEL="native bd-Postgres"
    if [[ -n "$PLUGIN_COMMAND" ]]; then
      [[ -x "$PLUGIN_COMMAND" ]] || die "BEADS_POSTGRES_PLUGIN_COMMAND is not executable: $PLUGIN_COMMAND"
      CANDIDATE_LABEL="bd-Postgres plugin"
    fi

    cat > "$WRAP" <<EOF
#!/usr/bin/env bash
set -euo pipefail
BD=$(shell_quote "$BD")
PG_URL=$(shell_quote "$BEADS_PG_TEST_URL")
PLUGIN_COMMAND=$(shell_quote "$PLUGIN_COMMAND")
PLUGIN_ARGS=$(shell_quote "$PLUGIN_ARGS")

export BEADS_POSTGRES_URL="\$PG_URL"
if [[ -n "\$PLUGIN_COMMAND" ]]; then
  export BEADS_BACKEND_PLUGIN_COMMAND="\$PLUGIN_COMMAND"
  export BEADS_BACKEND_PLUGIN_ARGS="\$PLUGIN_ARGS"
fi

if [[ "\${1:-}" = "init" ]]; then
  schema="w\$(printf '%s' "\$PWD" | md5sum | cut -c1-16)"
  has_p=0
  for a in "\$@"; do
    [[ "\$a" = "-p" || "\$a" = --prefix=* ]] && has_p=1
  done
  extra=(--backend=postgres --pg-url="\$PG_URL" --pg-schema="\$schema")
  [[ "\$has_p" = "0" ]] && extra+=(-p wp)
  exec "\$BD" "\$@" "\${extra[@]}"
fi

exec "\$BD" "\$@"
EOF

    echo "### clean slate: dropping accumulated w% schemas"
    psql "$BEADS_PG_TEST_URL" -tAc \
      "select 'drop schema \"'||schema_name||'\" cascade;' from information_schema.schemata where schema_name like 'w%';" \
      2>/dev/null | psql "$BEADS_PG_TEST_URL" >/dev/null 2>&1 || true
    ;;

  doltlite|doltlite-plugin)
    PLUGIN_COMMAND="${BEADS_DOLTLITE_PLUGIN_COMMAND:-}"
    PLUGIN_ARGS="${BEADS_DOLTLITE_PLUGIN_ARGS:-serve}"
    [[ -n "$PLUGIN_COMMAND" ]] || die "set BEADS_DOLTLITE_PLUGIN_COMMAND"
    [[ -x "$PLUGIN_COMMAND" ]] || die "BEADS_DOLTLITE_PLUGIN_COMMAND is not executable: $PLUGIN_COMMAND"
    CANDIDATE_LABEL="bd-DoltLite plugin"

    cat > "$WRAP" <<EOF
#!/usr/bin/env bash
set -euo pipefail
BD=$(shell_quote "$BD")
PLUGIN_COMMAND=$(shell_quote "$PLUGIN_COMMAND")
PLUGIN_ARGS=$(shell_quote "$PLUGIN_ARGS")

export BEADS_BACKEND_PLUGIN_COMMAND="\$PLUGIN_COMMAND"
export BEADS_BACKEND_PLUGIN_ARGS="\$PLUGIN_ARGS"

if [[ "\${1:-}" = "init" ]]; then
  "\$BD" "\$@"
  exec "\$BD" backend install doltlite --command "\$PLUGIN_COMMAND"
fi

exec "\$BD" "\$@"
EOF
    ;;

  *)
    die "unknown BEADS_DEEP_ORACLE_BACKEND '$BACKEND' (expected postgres or doltlite-plugin)"
    ;;
esac

chmod +x "$WRAP"

HARNESS_MODE="vendored"
if [[ -n "${BTS_RS_DIR:-}" ]]; then
  [[ -d "$BTS_RS_DIR" ]] || die "BTS_RS_DIR does not exist: $BTS_RS_DIR"
  HARNESS_MODE="external"
  rm -rf "$BTS_RS_DIR/crates/bts-conformance/testdata/golden"
else
  HARNESS_DIR="$REPO_ROOT/tests/oracle-a/harness"
  [[ -d "$HARNESS_DIR" ]] || die "vendored harness missing: $HARNESS_DIR"
  DEFAULT_CATALOG="$HARNESS_DIR/../../docs/scenarios/enumerated.json"
  if [[ -n "${BTS_CATALOG_FILE:-}" ]]; then
    [[ -f "$BTS_CATALOG_FILE" ]] || die "BTS_CATALOG_FILE does not exist: $BTS_CATALOG_FILE"
    export BTS_CATALOG_FILE
  elif [[ "${BEADS_DEEP_ORACLE_REQUIRE_CATALOG:-0}" = "1" && ! -f "$DEFAULT_CATALOG" ]]; then
    die "full enumerated catalog is unavailable; set BTS_CATALOG_FILE or BTS_RS_DIR"
  fi
  echo "### building vendored bts-rs harness"
  (cd "$HARNESS_DIR" && cargo build --release --bins) >/dev/null
  rm -rf "$HARNESS_DIR/testdata/golden"
fi

run_capture() {
  if [[ "$HARNESS_MODE" = "external" ]]; then
    (cd "$BTS_RS_DIR" && BTS_CATALOG=1 BTS_REFERENCE_BD="$BD" cargo run --release -q -p bts-conformance --bin capture_golden)
  else
    BTS_CATALOG=1 BTS_REFERENCE_BD="$BD" "$HARNESS_DIR/target/release/capture_golden"
  fi
}

run_scoreboard() {
  if [[ "$HARNESS_MODE" = "external" ]]; then
    (cd "$BTS_RS_DIR" && BTS_CATALOG=1 BTS_CANDIDATE="$WRAP" cargo run --release -q -p bts-conformance --bin scoreboard)
  else
    BTS_CATALOG=1 BTS_CANDIDATE="$WRAP" "$HARNESS_DIR/target/release/scoreboard"
  fi
}

echo "### backend: $BACKEND"
echo "### bd: $BD"
if [[ "$BD_BUILT" = "1" ]]; then
  "$BD" version 2>/dev/null | head -1 || true
fi
echo "### capturing goldens from bd-Dolt"
run_capture

echo "### scoring $CANDIDATE_LABEL against fresh goldens"
SCORE_OUT="$SCRATCH/scoreboard.out"
run_scoreboard | tee "$SCORE_OUT"

IN_LINE="$(grep -E '^\s*PASS:.*FAIL:' "$SCORE_OUT" | head -1 || true)"
IN_PASS="$(printf '%s' "$IN_LINE" | sed -E 's/.*PASS:\s*([0-9]+).*/\1/')"
IN_FAIL="$(printf '%s' "$IN_LINE" | sed -E 's/.*FAIL:\s*([0-9]+).*/\1/')"

echo
if [[ -z "${IN_FAIL:-}" || "$IN_FAIL" = "$IN_LINE" ]]; then
  die "could not parse scoreboard output"
elif [[ "$IN_FAIL" -eq 0 ]]; then
  echo "### RESULT: IN-SCOPE PASS ($IN_PASS scenarios, 0 divergences)"
else
  echo "### RESULT: IN-SCOPE FAIL ($IN_FAIL divergences; pass: $IN_PASS)" >&2
  if [[ -f /tmp/bts-failures.txt ]]; then
    echo "### failure details: /tmp/bts-failures.txt" >&2
    sed 's/^/  /' /tmp/bts-failures.txt >&2 || true
  fi
  exit 1
fi

echo "### DONE"
