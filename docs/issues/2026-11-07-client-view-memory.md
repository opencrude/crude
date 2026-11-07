# Each client keeps its own view (2026-11-07)

> Status: RESOLVED 2026-11-07. Contract:
> [client view memory](../reference/main-view-panes.md#client-view-memory).

## Reported

When the user came back to a workspace, Crude did not remember them: agents
they had minimized came back as open, active tabs. Quick view choices such as
showing hidden files in the tree were also lost. Every TUI client must keep
its own choices for its whole life, also when several clients use the same
workspace at the same time.

## Cause

- The agent view (minimized agents, opened sub-agents, selection) lived only in
  memory and was keyed by node pointers. A new shell or a workspace switch
  rebuilt it empty, and the first replica attach activated the first agent,
  which also un-minimized it.
- The tree's hidden-files toggle changed only the live tree model; a rebuild
  read the setting again.
- The rail order, sidebar widths, list positions and panel heights were stored
  in the shared workspace database through the core, so concurrent clients
  overwrote each other.

## Fix

- One private record per client profile and workspace in the client-state
  store (`clientstate.LoadViewMemory` / `SaveViewMemory`), the same exclusive,
  epoch-fenced profile that already owns composer drafts and editor recovery.
- The shell installs the record before the first inventory arrives, so agents
  adopt the remembered choice the first time they appear. Minimized agents
  never open by themselves; the remembered agent gets the focus again.
- One save trigger per Update; serialized, version-ordered writes; flushed on
  workspace switch and shutdown.
- The workspace-database layout store and its two core methods
  (`workspace.tool_layout`, `workspace.tool_layout_save`) are removed.
