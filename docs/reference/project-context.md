# Retained Project Artifact Context

> Status: Current initial observation owner. Provider configuration binding,
> autonomous selection, quantitative admission and product inspection remain open.

## Owner and scope

`Workspace.ProjectContext` owns one `ProjectContextService` for a retained
workspace generation. It consumes explicit inert artifact selections through
the passive compilation-database and CMake File API importers in
`insight/project/` and `insight/project/cmake/`. Those libraries still perform no
filesystem access, path resolution, shell tokenization or execution.

The service is not a second source document, semantic index or language-server
fleet. It does not infer compiler invocations, select a build configuration,
run metadata commands, resolve response files or follow JSON references as
filesystem permissions. Parsing a command preserves it as opaque data. The
calling workspace operation must already have authority to read the selection;
the selection and its digest grant neither read nor execution permission.

Construction is idle. There is no recursive discovery, automatic selection,
configuration UI or agent control entry point for this foundation yet.
Neither Static only nor enabling Language intelligence starts its observation.
Promotion to autonomous selection/provider use must first connect the shared
quantitative governor and the existing capability/policy owner. No second
installer, process launcher or Trust decision belongs here.

## Selection and observation

Each selection contains stable set IDs and native workspace-relative paths.
A compilation database is one explicit file. A CMake set supplies one selected
index and every reply artifact it permits the importer to consume. A referenced
but unselected file remains unavailable even when it exists on disk. There is
no newest-index guess or source/build path traversal from imported metadata.

Selection is detached and canonicalized before publication. Duplicate IDs/paths,
outside-root, unclean, empty or unsafe paths and incompatible set shapes are
refused without changing the accepted selection. Equal selections do not cause
another read or generation change. Empty selection invalidates former evidence
without creating a clean-project result or performing another read.
Nested reply counts and operand byte bounds are checked before cloning/sorting,
including invalid declarations. Actual native duplicate file identities are
refused off-thread; distinct files are never merged by case-folding their names.

One owned worker coalesces pending selections and invalidations. It observes
native root identity, reads only selected regular files and imports bounded
metadata off the actor and Update goroutines. Resolution is confined through an
opened OS root; observed file/ancestor symlinks and nonregular objects are refused.
Opened-handle, path and ancestor identities are checked during each read, then
the complete selected set and root are rechecked after derivation. A changed
root cannot silently rebind a previously accepted owner.

This is cooperative filesystem race detection, not a transactional filesystem
snapshot or a sandbox against malicious same-user changes. The root reader's
five-second context is a cooperative deadline, not an interruptible kernel-I/O
guarantee. Shutdown joins the actual worker even when an OS read cannot yet
return. Unix opens use nonblocking mode so a FIFO replacing a regular file does
not wait for a writer; that is not a regular-file latency bound.

Bounds are 128 selected files, 4096 bytes per relative path, 16 MiB aggregate
input bytes, and the importers' tighter format/work limits. Compilation-database
accounted work additionally shares one 16 MiB budget across the selection.
These are byte/model-work bounds, not exact heap or RSS containment. The former
accepted model and one in-flight replacement can coexist. No unbounded event
queue, per-event worker or source-text mirror is retained.

## Generations and stale evidence

`ProjectContextSnapshot` separates the current input generation from its last
accepted observation generation. Artifacts identify the exact bytes and byte
count, including whitespace; they do not establish producer authenticity.
Snapshots detach their slices. Imported models are immutable with detached
exports and can be obtained as current only for the exact ready generation.

Invalidation is synchronous and performs no file I/O. It marks former evidence
pending/stale before scheduling observation. Failure retains the previous
identities as stale instead of clearing them to make the project look clean.
Late results for a superseded selection, cancelled observation, closed owner or
replacement root cannot publish current data. Error summaries never echo raw
artifact contents, arguments or private-path-bearing native errors.

Ready means only that the selected inert artifacts were observed. Unknown build
graph, generated-input freshness, producer/toolchain/environment identity and
watcher completeness remain explicit. No result from this service supplies a
project-check denominator, complete dependency coverage or semantic correctness.

## Core ingress and lifetime

The retained core file watcher routes exact file facts and coarse resyncs to
this owner independently of shell subscribers. This initial owner conservatively
invalidates its selection on every workspace file fact: event spellings cannot
prove native file identity across case-sensitive/insensitive directories, Unicode
normalization or short-name aliases. It does not add a second path-string filter
that can silently discard a delivered relevant event. This may recompute after an
unrelated event; one worker and one latest pending selection coalesce bursts.
The watcher owns workspace event scope. Event paths never grant additional reads.
The owner installs neither another watcher nor a shell callback. Notifications
remain hints, not proof that no external changes were missed.

Workspace replacement and shutdown cancel and join the old owner. A replacement
gets the canonical source service's new runtime generation, never the old
artifact selection or model. Idle core handover refuses an active/queued
observation and reserves an idle owner against new selections. Passive
invalidations arriving during that reservation remain stale metadata intent;
rollback wakes one recomputation rather than losing the event or replaying an
effect.

The real retained-core watcher regression
`TestRetainedCoreWatcherInvalidatesSelectedArtifactsWithoutAShell` exercises a
selected artifact changing with zero attached clients. Workspace tests exercise
exact identities, explicit CMake input sets, unknown coverage, aggregate-set
revalidation, stale retention, coalescing, blocked-worker joins, idle rollback
and root/workspace replacement. These are owner/lifecycle checks, not rendered
IDE, real-language, all-platform, provider-restart or complete-context acceptance.

Remaining product work is tracked in
[autonomous code intelligence](../plans/autonomous-code-intelligence.md):
discover/select inspectable configurations, admit reads/derivation through the
quantitative governor, populate immutable root-bound provider scope tables,
restart providers when those bindings change, and expose this qualified evidence
through the common editor/agent/control views.
