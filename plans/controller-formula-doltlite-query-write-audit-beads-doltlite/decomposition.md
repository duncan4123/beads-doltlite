# Decomposition: DoltLite Controller/Formula Query-Write Audit

## Source

- Source bead: `bd-5tj` - Critically audit controller/formula DoltLite query-write coverage.
- Plan artifact: `plans/controller-formula-doltlite-query-write-audit-beads-doltlite/plan.md`.
- Implementation convoy: `bd-7po`.
- This is a document-only decomposition for the jj-managed build workflow. The runnable work units are Beads tasks tracked by `bd-7po`.

## Goal

Produce a concrete audit matrix proving every controller/formula read and write path used by Gas City formula-driven work under the beads-doltlite backend. Each operation must have either a named proof or a named gap with the exact test file/function and command needed to close it.

## Work Units

### `bd-0r3` - Inventory controller and formula DoltLite query/write operations

Build the exact operation inventory for the audit matrix.

Scope:

- Owners: gascity core, gascity-jj-base pack, gc pack, gastown helpers, and beads-doltlite.
- Lifecycle steps: formula materialization, route stamping, controller desired-state build, pool scale demand, session start/restart/resume, worker hook claim, continuation sibling assignment, drain acknowledgement, finalization, teardown, recovery, and document/workspace metadata operations.
- Matrix fields: owner, lifecycle step, exact operation, predicate or mutation fields, proof mode, and initial result.

Acceptance:

- Every controller/formula read and write path has a matrix row.
- Rows name exact commands, jq filters, Go store calls, DoltLite SQL queries, or write mutations.
- Rows cover status, assignee, issue_type, is_blocked, dependencies, `gc.routed_to`, `gc.run_target`, `gc.kind`, `gc.session_affinity`, `gc.root_bead_id`, `gc.root_store_ref`, `gc.continuation_group`, drain metadata, and document/workspace metadata.

### `bd-8x0` - Build DoltLite parity proof fixture for audit operations

Create or extend the DoltLite-backed fixture and proof harness used by the audit.

Scope:

- Compare each named read path against both `bd` and `doltlite-client query`.
- Prove writes are accepted by beads-doltlite, persisted in DoltLite, and visible to the next lifecycle query.
- Include ready, blocked, assigned, unassigned routed, control-dispatcher, drain/no-work, restart/resume, and continuation-group states.

Acceptance:

- Fixture or harness can reproduce the operation matrix states.
- Each read path has a bd-vs-doltlite-client comparison or a named gap.
- Each write path has persistence and next-query visibility evidence or a named gap.

### `bd-9xd` - Prove formula lifecycle writes and route qualification behavior

Audit formula-created work and lifecycle metadata writes end to end.

Scope:

- Formula-created beads and continuation siblings.
- Route stamping and target qualification behavior.
- Metadata fields: `gc.routed_to`, `gc.run_target`, `gc.kind`, `gc.session_affinity`, `gc.root_bead_id`, `gc.root_store_ref`, `gc.continuation_group`, drain metadata, and document/workspace metadata.
- Fully qualified route examples such as `lightjj/gc.requirements-planner`.
- Short `gc.run_target` values only where the formula or controller intentionally consumes short names.

Acceptance:

- Formula-created work is covered, not only manually created beads.
- Fully qualified routes and intentional short targets are explicitly distinguished.
- Drain acknowledgement, finalization, teardown, and recovery writes have proof or named gaps.

### `bd-b26` - Audit pack shell-outs and helper scripts that call bd

Inspect shell-based formula and helper integration points.

Scope:

- gascity core packs.
- gascity-jj-base.
- gc pack.
- gastown helpers.
- beads-doltlite scripts involved in formula-driven controller work.

Acceptance:

- Every pack/helper `bd` shell-out used by formula workflows is represented in the matrix.
- Rows include exact command shape, filters, expected fields, and proof mode.
- Missing coverage identifies the proposed test file/function and exact validation command.

### `bd-iz0` - Publish DoltLite controller/formula query-write audit report

Consolidate the final audit deliverable after the proof-gathering beads complete.

Dependencies:

- Blocks on `bd-0r3`, `bd-8x0`, `bd-9xd`, and `bd-b26`.

Acceptance:

- Final report contains the full audit matrix.
- Every controller/formula operation has named proof or a named test gap.
- Each missing proof includes proposed test file/function and exact command with required tags/libs.
- Each behavioral divergence has a linked implementation bead.
- The report classifies the ready-work issue as fast-path visibility, query construction, route qualification, scale/reconciler demand, session materialization, or another evidenced cause.

## Dependency Shape

- `bd-7po` tracks all runnable work units: `bd-0r3`, `bd-8x0`, `bd-9xd`, `bd-b26`, and `bd-iz0`.
- `bd-iz0` depends on the four proof-gathering beads.
- The implementation drain should use `bd-7po`, not the launch/source convoy `bd-vva`.

## Validation Plan

Expected local validation commands for implementation workers:

```bash
make test
CGO_ENABLED=1 go test -tags gms_pure_go ./internal/storage/doltlite ./internal/storage/issueops ./internal/storage/sqlbuild ./cmd/bd/...
```

Audit-specific proof commands should be recorded in the final report next to each matrix row. DoltLite parity rows must include both the `bd` command and the equivalent `doltlite-client query` or `doltlite-client exec` command.

## Notes And Ambiguity

- The workflow root metadata referenced a requirements artifact change ID that was not resolvable in the current jj workspace during decomposition. The current checkout did contain the implementation plan and manifest, so this decomposition is based on `plan.md` plus the source bead `bd-5tj`.
- The decomposition intentionally creates audit/report tasks, not direct source-change tasks, because the approved source bead asks for a critical audit report with proof classification and follow-up beads for any divergences.
