# Managed Execution Contexts

> Status: Current service and control contract. Parent-owned hygiene, reviewed integration/discard and explicit recovery are implemented; native platform coverage remains separate acceptance.

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
coupled and non-Git tasks stay shared with a stated reason. Shared execution is a
binding, not a new snapshot or retained worktree record. Under an isolated parent
it pins that exact checkout for the child's run. Explicit worktree
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

Native file mutations in a managed checkout are checked against that checkout
independently of optional file ownership. Absolute shared-checkout operands,
`..` escapes and symlink-resolved escapes are rejected before claims or effects;
move sources and destinations and all patch operands are checked together. Only
the owning agent's private scratch directory is exempt. Launch, live claim repair
and interrupted-child continuation enforce the same checkout boundary; relative
child claim repairs resolve against the child's checkout, not the parent's logical
workspace. Explicit shared execution retains normal external-path authority.
This is not an OS sandbox: shell commands and external MCP effects can still
write outside the checkout and are not constrained by native ownership checks.

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

There is no execution-count admission or reopen limit. The retained-count,
age and snapshot-storage thresholds are configurable hygiene warnings, never
launch blockers. Snapshot safety bounds remain 20,000 baseline files, 256 MiB
total and 32 MiB per file. They bound one isolated capture, not a pool of launch
slots, and never forcibly prune work. Format changes require backed-up manual
alignment; no legacy reader or automatic upgrade.

## Review, apply, undo and crash recovery

Discover exact `execution.*` schemas via [control](control-contract.md). Current
commands include selection/select, list/create/inspect/review/apply,
reconcile/settle/keep/cleanup,
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

## Parent ownership and workspace hygiene

The parent owns final child disposition, including failed or cancelled attempts.
Read its final report and authoritative terminal outcome, then review and apply
useful work, or review and explicitly discard unwanted work. Completion never
automatically merges or deletes a checkout. Capable peers and children may also
perform permitted janitorial work on other parents' settled executions in this
workspace. Contact the exact owner through `agent_send` if intent is unclear;
age, silence and offline presence alone do not prove abandonment.

- `execution.list` and `execution.inspect` expose compact identity, roots,
  readiness, pins and storage facts, not whole repository snapshots.
- `execution.reconcile {id}` releases only pins whose exact runs are terminal
  and whose local work, saved selections, queued input and open recovery no longer
  retain that checkout. It returns retained reasons, never guesses or kills work.
- A genuinely unwanted parked task can be cancelled with `execution.settle`
  using its root session, exact agent, recovery ID and generation. Open its saved
  session with `session.open` first if cold. The existing recovery owner preserves
  transcript/history and refuses live, replaced or unrelated incidents. This is
  an explicit disposition decision, not automatic age-based abandonment.
  If launch committed only the child checkpoint before parent publication failed,
  settlement adopts that exact checkpoint after proving its parent/session,
  execution, incident/generation and terminal run. It never replaces an existing
  parent incident or open recovery, and never starts the obsolete task.
- `execution.keep {id, keep:true|false}` preserves intentional retention or clears
  it after that intention is resolved. A reason belongs in the report/handoff.
- Ordinary `execution.cleanup` removes a settled clean or integrated result.
  Unintegrated newer work remains protected. For unwanted work use
  `execution.review {id, discard:true}`, then cleanup with `discard:true` and its
  exact `discard_digest`. This source-only review works even when the logical
  destination has conflicting intentional human edits. It includes source HEAD
  and ignored bytes; changes since review refuse removal.

Reviewed discard durably archives actual regular-file results before removal,
including ignored output and preparation-incomplete remnants. Failed preparation
is never runnable and does not masquerade as intentional Keep. Missing, partial
and linked setup remnants use the same review/discard controls. Applied Git
filters are rejected before status or staging so cleanup cannot execute a repository's
clean/process driver. Unsupported/symlink/corrupt states retain evidence and say
why rather than forcing arbitrary removal.

Active pins, live commands/terminals, unsaved or unresolved checkout documents,
selected executions, queued inputs and recoveries remain protected. Cold saved
root/child session references count too. Selection, explicit checkout-input
commit, terminal admission and final retention-check/removal share the mutation
boundary. Document ownership stays locked through cleanup; save or resolve
accepted checkout text before review. Intentional logical-project text is never
an isolated child's delta. Integrated cleanup still verifies its durable
committed/undone receipt and Local History safety snapshot.

Removed executions retain a compact ledger with exact provenance and archive
reference; baseline/result bytes move to an on-demand archive, not live inventory
or every model warning. No passive startup cleanup or archive expiry is implied.
Undo evidence is not deleted to meet a warning threshold.

The early root/child system contract and context-only post-child outcome reminder
state this workflow once. Request-boundary inventory notices are deduplicated;
they are not new human requests, dialogs, repeated progress messages or permission
to delete. See [settings](settings.md#worktree-hygiene).

## Evidence and limits

`worktree/service_test.go`, `parent_baseline_test.go`, `integration_test.go`,
`workspace/documents_execution_test.go`, `execution_parent_test.go`,
`session/worktree_e2e_test.go`, `parent_execution_test.go`, the hygiene regression
tests in worktree/session/core/workspace, and core execution journey
suites cover focused service and product paths. Test presence is not a claim that
all gates currently pass. Native Windows filesystem/Git behavior, SSH host locality
and distinct-host execution remain separate acceptance.
`app/live_execution_hygiene_test.go` is opt-in actual-model acceptance through the
framed core. It forbids shell/manual Git escape, seeds conflicting failed results,
stale pins, cold parked recovery, intentional Keep/live work and manual edits, then
requires cleanup, another researcher child and continued task output. Its optional
connected TUI mode uses the repository visual capture workflow. Passing selected
models is evidence for those journeys, not a guarantee of universal autonomy.
The hygiene journey was exercised with configured GPT Sol 6.1 and DeepSeek 4.1
Flash accounts, including connected Linux TUI captures at 160x40 and 80x24.
Settings controls were inspected at both sizes, with an explicit zero threshold
saved through the UI and persistence/inheritance checked across core restart.
Research designs motivate isolation; they do not guarantee every repository,
setup command, dependency, filter or developer workflow is supported.
