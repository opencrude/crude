# Desktop notifications

> Status: Current reference for native desktop notifications, shell presence
> and the notifying-shell election.

Crude tells a user who is away from it that an agent needs an answer or an
approval, stopped, or finished. It uses the operating system's own
notifications: toast notifications on Windows and freedesktop.org
notifications (D-Bus) on Linux. macOS has one reserved slot and is not
implemented yet.

The goal is one useful message, never noise: Crude does not notify a user who
is looking at it, never announces something the user already saw, shows one
message per workspace that is replaced in place, and removes it when it is no
longer true.

## Owners

| Concern | Owner |
|---|---|
| Delivery: show, replace, withdraw, click | `desktopnotify/` (one `Center` per shell process) |
| Which shell notifies | `core/presence.go`, published on `state.presence` |
| What is news, the message text, banner or quiet update | `tui/viewmodel/desktop_notices.go` |
| The user's choice | `ui.desktop_notifications` setting |
| Source data | the replicated status center (`AgentStatusSnapshot`) |

There is no second notification store. In-app notices stay in the status
center; desktop notifications are derived from the same agent status and keep
no history of their own.

## Local Crude, remote Crude and many shells

A desktop notification is a client-local effect, like the clipboard. The core
never shows one, because the core can run on a server without a desktop or on
somebody else's screen.

| Setup | Who notifies |
|---|---|
| Local: shell and core on one computer | The shell, on this desktop. |
| Remote: `crude --ssh HOST` or a paired direct connection | The shell, on this desktop. The remote host's desktop is never used. |
| Crude started inside an SSH login (`ssh host`, then `crude`) | Nobody. The shell runs on the remote host, so a notification would show on that host's screen. `desktopnotify` reports `ErrRemoteSession` when `SSH_CONNECTION`, `SSH_CLIENT` or `SSH_TTY` is set. Use `crude --ssh HOST` to run the shell on this computer. |
| Several shells attached to one workspace (two windows, or a desktop and a laptop) | Exactly one shell, elected by the core. |

### Presence and election

Each shell decides whether its own user is **present**: the terminal has
focus (focus reports, including Windows console focus events) and the user
acted (key, mouse, paste, focus gain) within **Away after** (5 minutes by
default); the shell's start counts as an action. A terminal that keeps focus
while its user walks away therefore becomes away after that time.

The shell sends `shell.presence` with two facts when either changes, and again
after reconnect:

- `present` — the user is present in this shell.
- `notifies` — this shell can show desktop notifications (the platform
  backend reported Ready) and the user wants at least one kind of news.

The workspace actor keeps one entry per attached connection, stamped with the
core's own clock at each presence change, and publishes a `PresenceSnapshot`:

1. If any shell is present, `Present` is true and nobody notifies: the user
   sees Crude.
2. Otherwise the notifier is the shell that can notify and whose user left it
   last — the computer where the user most likely is. Equal times choose the
   larger owner, so the election is deterministic.
3. A detached shell leaves the election at once.

A shell shows notifications only while the snapshot names its own connection
and nobody is present. A shell that cannot notify (SSH login, macOS, no
notification service, notifications turned off) is never elected, so another
shell can take over.

## What is news

| Kind | Status | Agents |
|---|---|---|
| Needs your answer | `NeedsAction` in a question | Every agent, children too |
| Needs your approval | `NeedsAction` in an approval | Every agent, children too |
| Stopped | `needs_attention`, `interrupted`, `failed` | Agents the user started (no parent), not the workspace Aeron |
| Finished | `completed` | Agents the user started, not the workspace Aeron |

Each kind has its own switch in Settings; a switched-off kind is never news.

A cancelled run is not news: the user cancelled it. A child's finish or stop
goes to its parent, not to the user.

The shell watches status changes and records when each condition appears. A
condition is news only if it appeared while the user was away:

- The status that exists when the shell attaches is the baseline; it is never
  announced.
- A condition that appeared while the user was present anywhere is seen, and
  stays seen after the user leaves.
- When the user comes back (this shell present, or the election reports
  presence), everything that exists is seen.
- When a focused terminal becomes away by idleness, conditions that appeared
  after the last input are unseen.

## The message

One message per workspace, keyed by the workspace root:

| News | Title | Body |
|---|---|---|
| One question | `Ivan needs your answer` | workspace name |
| One approval | `Ivan needs your approval` | workspace name |
| One stop | `Ivan stopped` | reason · workspace name |
| One finish | `Ivan finished` | workspace name |
| Several | `2 agents need you, 1 finished` (or `3 agents finished`) | up to three lines, most urgent first, then `N more`, then the workspace name |

- **Settle.** A change waits 1.5 seconds before it is shown, so a request that
  is approved at once makes no message and a burst makes one.
- **Banner or quiet update.** New news shows a banner with sound. Within 30
  seconds of a banner, further news only updates the message quietly
  (Windows `SuppressPopup`; freedesktop low urgency and `suppress-sound`),
  unless the news is more urgent than what the message showed.
- **Withdraw.** The message is removed from the screen and from the
  notification list when the user comes back, when the condition ends (for
  example the question is answered in another shell), when another shell is
  elected, when Settings exclude it, on workspace switch, and when the shell
  exits normally. A shell process that is killed cannot withdraw; Windows
  keeps its last message in the notification list until the user clears it
  or a later Crude shell replaces the same workspace message.
- **Click.** A click opens the most urgent agent in Crude, brings the terminal
  window to the front where the platform allows it, and removes the message.

## Settings and test

**Settings → Notifications** (IDE group, after Terminal) edits
`ui.desktop_notifications`. It is an application preference that a workspace
can override, for example to turn notifications off for one project. Only
explicit choices are stored; a missing switch is On.

| Field | Path | Default | Effect |
|---|---|---|---|
| Desktop notifications | `enabled` | On | Off withdraws the message and dims the other choices. The shell reports that it cannot notify, so another shell can be elected. |
| Questions and approvals | `input` | On | Needs your answer and needs your approval. |
| Stopped runs | `stopped` | On | Stops with an error, a limit or an interruption. |
| Finished work | `finished` | On | Finished turns of agents that the user started. |
| Sound | `sound` | On | Off shows the banner without the system sound (Windows `<audio silent="true"/>`; freedesktop `suppress-sound`). |
| Away after | `away_minutes` | 5 | Minutes without input before a focused terminal counts as away; 1 to 60. |

With all three kinds off, the shell also reports that it cannot notify.
Changes apply at once: a shown message that the new choices exclude is
withdrawn.

The palette command **Test Desktop Notification** (`notifications.test`) shows
a sample at once, without the presence rules, so the user can check the
system's own notification settings. It follows the Sound choice. If this
shell cannot notify, it shows an in-app notice that says why and what to do.

## Platforms

### Windows

`desktopnotify/toast_windows.go` and `winrt_windows.go` call the Windows
Runtime toast API directly through `combase.dll`, without cgo, PowerShell or
generated projections. Interface IDs and method slots come from the Windows
SDK headers `winrt/windows.ui.notifications.h` and
`winrt/windows.data.xml.dom.h`.

- Identity: the per-user AppUserModelID `Crude`, registered under
  `HKCU\Software\Classes\AppUserModelId\Crude` with `DisplayName` and
  `IconUri` (the PNG written to `.crude/notifications/icon.png`). This is the
  unpackaged-app registration that the Windows App SDK uses: no administrator
  rights, no shortcut, no COM server. Windows Settings → Notifications lists
  Crude under that name.
- Content: the `ToastGeneric` template built with `encoding/xml`, so agent
  text can never add elements or attributes.
- Replace and withdraw: `Tag` (a hash of the key) and `Group` `crude`;
  `ToastNotificationHistory.RemoveGroupedTagWithId` withdraws.
- Blocked: `ToastNotifier.Setting` reports a user or policy switch. Windows
  answers `ERROR_NOT_FOUND` for an unpackaged ID until the user changes that
  switch; that answer means "allowed".
- Click: the toast's `Activated` event, while the shell runs. The handler is a
  static object that never moves or frees. It raises the console window's root
  owner: the `CASCADIA_HOSTING_WINDOW_CLASS` window in Windows Terminal, the
  `ConsoleWindowClass` window in the classic console.
- Threading: every call happens on the Center's one OS-locked worker thread in
  the multithreaded apartment.

### Linux

`desktopnotify/freedesktop_linux.go` speaks the
[Desktop Notifications Specification 1.2](https://specifications.freedesktop.org/notification-spec/latest/)
over the session bus with `github.com/godbus/dbus/v5`. GNOME, KDE Plasma,
Xfce, Cinnamon, dunst, mako and swaync implement it.

- No bus auto-start: without a session bus there is no desktop to show on.
- `GetCapabilities` decides `actions` and `body-markup`; with markup, the
  body is escaped.
- `replaces_id` replaces in place; `CloseNotification` withdraws;
  `NotificationClosed` forgets a message that the user closed.
- Signals are matched by the service's bus name, object path and interface,
  so another program cannot fake a click.
- Every call has a 5-second deadline, so a stalled service cannot stop the
  worker.

### macOS

Not implemented. `desktopnotify/darwin.go` returns `ErrUnsupported` and holds
the plan: `UNUserNotificationCenter` needs a signed application bundle, so the
backend must live in a small signed helper bundle with the same three
operations (show with replace by key, withdraw by key, click by key). Do not
use `osascript display notification`: it shows Script Editor as the sender.

## Packages considered

| Package | Decision |
|---|---|
| `git.sr.ht/~jackmordaunt/go-toast/v2` | Rejected: advisory GHSA-c8qg-xhgr-798r (its PowerShell fallback runs `$(...)` in notification text as code; no fixed release), no tag/group, no withdraw. |
| `github.com/gen2brain/beeep` | Rejected: uses go-toast on Windows (same advisory) and supports only title, body and icon. |
| `github.com/saltosystems/winrt-go` | Rejected: its generated projections lack the toast interfaces. |
| `github.com/esiqveland/notify` | Not used: it matches signals without the sender (any program could fake a click) and makes calls without a deadline. godbus, which it wraps, is used directly for the spec's four methods. |
| `github.com/godbus/dbus/v5` | Used on Linux; already in the module graph through the keyring. |

## Tests

- `desktopnotify`: Center coalescing, withdraw and close, retry of a missing
  service, click routing, text cleaning, SSH detection; Windows XML escaping
  and tags; Linux against a fake notification service on a real session bus
  (run the Linux test binary inside `dbus-run-session`).
- `core/presence_test.go`: election rules.
- `tui/viewmodel/desktop_notices_test.go`: baseline, seen news, election,
  settle, cooldown, withdraw, idle, click, workspace switch, kind switches,
  sound and away delay.
- `tui/settings/notifications_test.go`: page position, defaults stored as
  nothing, round trip, master switch, away range, page reset ownership.
