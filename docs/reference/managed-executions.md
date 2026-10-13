# Managed Execution Contexts

> Status: Current service and control contract — conservative Git isolation, reviewed integration and recovery are implemented; platform and complete UI acceptance remain open.

## Logical workspace versus execution checkout

`worktree.Service` owns current-format manifests and bounded checkout artifacts
under the workspace data directory, disjoint from project and Git administration.
`workspace.ExecutionService` borrows the logical workspace's accepted document and
Local History owners; an artifact is not another settings/workspace authority.
`session/` captures the exact execution selection for new input and delegates via
`agent_start isolation: auto|shared|worktree`. Selection changes never move a live
run or already queued input. Relative native paths, commands, guidance, claims,
MCP roots and Git operations use the resolved execution root. Logical settings,
collaboration and accounting retain the logical workspace identity.

Auto isolates supported independent delegated writers in Git projects. Read-only,
coupled and non-Git tasks stay shared with a recorded reason. Explicit worktree
requires a supported Git checkout; explicit shared preserves deliberate partitioned
editing. This is a conservative policy, not universal automatic task analysis or
a security sandbox. File claims cannot constrain opaque command/MCP effects.
Use the service, not ad hoc shell `git worktree` setup or merge improvisation.

## Faithful baseline

Creation captures the current dirty tracked/untracked files and accepted shared
document revisions, including unsaved **accepted** text, not private shell drafts.
Ignored paths require explicit inclusion (accepted documents are included).
Two bounded captures must agree; HEAD and common Git ownership are checked.
A child of a selected managed execution uses the current parent's exact
`SourceContextID` checkout, including its newer dirty bytes, rather than stale
logical-workspace document overlays. Missing/foreign/unready source contexts fail
closed. Absolute logical-checkout file claims cannot silently rebase into a new
checkout; relative claims bind its exact resolved root.

Git preparation disables hooks/fsmonitor and removes inherited `GIT_*` controls;
a detached no-checkout worktree plus captured bytes avoids executing setup hooks
or filters. A configured Git filter driver that an attribute applies to a
copied path (tracked or explicitly included) fails setup; merely configured
drivers, such as the `filter.lfs` entries Git for Windows installs system-wide,
execute nothing and are accepted. Unresolved index state, submodules, symlink/
nonregular baselines and alternate commit bases fail conservatively. The supplied
base must resolve to local HEAD. This does not reproduce the original index's
staging state as a separate authority. Git administration (`.git` pointer files,
common directories and linked-worktree administration) is excluded from capture,
review, integration and Local History, even when ordinary ignore filtering is off.

Defaults bound 16 active contexts, 20,000 baseline files, 256 MiB total and 32 MiB
per file. Exhaustion refuses creation, never forcibly prunes retained work. Format
changes require backed-up manual alignment; no legacy reader or automatic upgrade.

## Review, apply, undo and crash recovery

Discover exact `execution.*` schemas via [control](control-contract.md). Current
commands include selection/select, list/create/inspect/review/apply, keep/cleanup,
integrations/undo/recover. Resource targets must match the exact context or operation;
trusted gateway caller attribution supplies owner identity, not caller-authored
owner fields. Review produces an immutable digest covering source delta and exact
merged accepted-document/disk postimages. Independent nonoverlapping later work
can be retained; overlaps/changed preimages fail, not guessed merge success.

Apply uses the shared document/mutation serialization boundary, persists a prepared
integration journal and mandatory Local History safety snapshot, checks exact
reviewed preimages, then commits disk and accepted document state. Failures roll
back; uncertain/prepared states remain explicitly inspectable. Undo is exact
operation-scoped inverse integration with current preimage checks and safety
history, not checkout/reset or whole-project restoration. Explicit recovery
reconciles the original journal; passive startup never replays prepared effects.
Unfinished integration blocks new creation/apply until explicit recovery.

Run pins, Keep, preparation failures, unintegrated newer changes and recovery
receipts retain artifacts. Cleanup requires exact owner, archived source evidence,
durable committed/undone integration receipt and valid safety backup; it refuses
active pins and newer work rather than forcing Git removal. Crash-left pins are
not automatically cleared during passive Open. Stopped/unfinished agents retain
reviewable evidence; completion does not imply integration.

## Evidence and limits

`worktree/service_test.go`, `parent_baseline_test.go`, `integration_test.go`,
`workspace/documents_execution_test.go`, `execution_parent_test.go`,
`session/worktree_e2e_test.go`, `parent_execution_test.go` and core execution journey
suites cover focused service and product paths. Test presence is not a claim that
all gates currently pass. Native Windows filesystem/Git behavior, SSH host locality,
distinct-host execution and full UI/capture coverage remain separate acceptance.
Research designs motivate isolation; they do not guarantee every repository,
setup command, dependency, filter or developer workflow is supported.
