# Code layout and naming

> Status: Current reference. Enforced by `tools/layoutcheck`, `.golangci.yml` and the pre-push hook.

This is where Go code goes in Crude and how it is named. A machine checks
every rule that a machine can check. Each gate error names its rule and links
to the section below, so read the section, apply the fix, and run the gate
again. The rules follow the Go documents and large Go projects listed in
[Sources](#sources).

## Run the gates

```bash
make hooks          # once per clone: run the gates before every git push
make conventions    # layout, names, lint and doc comments on new code
make layout-check   # only the folder, package, file and name rules
```

`make design-audit` and the Conventions CI workflow also run these checks.

<a id="allow-unused"></a>
A reviewed exception goes in `tools/layoutcheck/allow.txt` as
`RULE<TAB>path<TAB>name<TAB>reason`, where name is `-` for a path rule. The
reason must let a reviewer check the decision. An exception that no longer
matches anything fails the gate (ALLOW-UNUSED): delete the line.

## The tree

```text
main.go       the crude command (go run .)
app/          CLI, startup and core process lifecycle
core/         the per-user core; coreclient, coreprotocol, corewire, shellport
workspace/    per-project state: documents, files, LSP, terminals, Git
agent/        group: turnloop, session, provider, prompt, tools and the rest
tui/          the shell UI: viewmodel, editor, screens, ui, theme
insight/      group: code-intelligence data, project facts, native syntax
lsp/          group: language-server transport and result normalization
text/         group: text algorithms (diff, search, encoding, indentation)
types/        group: neutral contracts by subject (setting, conversation, ...)
internal/     group: shared support with no product rules
tools/        group: development programs (wiregen, namesgate, layoutcheck)
assets/ docs/ scripts/ site/ third_party/ .github/ .githooks/
```

The [code map](../architecture/code-map.md) names the owner of each package.

## Folder rules

<a id="layout-top"></a>
### LAYOUT-TOP: only the listed top-level entries

The root holds the folders above, `main.go`, and module, build and tool
configuration files. Put new code in the folder that owns it. A new top-level
entry is a design decision: add it to `topLevel` in `tools/layoutcheck` and to
the tree above in the same change.

<a id="layout-group"></a>
### LAYOUT-GROUP: group folders hold packages, not files

`agent`, `insight`, `internal`, `lsp`, `text`, `tools`, `types` and
`tui/screens` only arrange packages, like Go's `src/go` and Kubernetes `pkg`.
Put a Go file in the child package that owns it, or make a new child package.

<a id="layout-child"></a>
### LAYOUT-CHILD: a package folder holds only its parts and extensions

In Go a folder is one package, so a package with sub-packages always shows its
own files beside its child folders. That is normal when the children belong to
it: the parent imports them (its parts), or they build on it or on one of its
parts (its extensions, such as a client or a test helper). `net/http`,
Kubernetes `pkg/kubelet` and gopls `internal/cache` work this way. A child that
the parent does not use and that does not build on the parent belongs
somewhere else; if most children are like that, the folder must be a group.

<a id="layout-internal"></a>
### LAYOUT-INTERNAL: internal packages are shared support

Packages under `internal` hold support code with no product rules: build
identity, logging, metrics, network transport, retry, clipboard, console and
terminal parsing. They never import `app`, `core`, `agent`, `workspace` or
`tui`. A package that needs one of those layers lives under that layer.

<a id="third-party"></a>
### THIRD-PARTY: one folder and one note per upstream project

`third_party` holds code Crude did not write. Each folder is named after its
upstream module and contains the upstream license and one `CRUDE.md`: the
upstream URL, the base version, every local change, and how to update. Forks
never go in `vendor`, which `go mod vendor` owns.

## Package names

<a id="name-package"></a>
### NAME-PACKAGE: lowercase letters and digits

Join words without separators: `tabwriter`, `prompthistory`. No underscores
or mixedCaps.

<a id="name-folder"></a>
### NAME-FOLDER: the package name is the folder name

`encoding/base64` is `package base64`. When a short name would collide with a
sibling that callers use together, put the subsystem in the name and in the
folder, as `net/http/httptest` does: `core/coreclient`, `lsp/lspclient`.

<a id="name-meaning"></a>
### NAME-MEANING: the name says what callers get

Never `util`, `common`, `helper`, `misc`, `model`, `api`, `types` or
`interfaces`. Do not pick a name that callers use for local variables
(`settings` and `status` are common locals in Crude, so the packages are
`setting` and `statuscenter`). Split a package by subject, not by size; most
Go packages have one to three files.

## File names

<a id="file-name"></a>
### FILE-NAME: lowercase words joined by underscores

`core_handover.go`, `find_replace.go`. In a broad package every file starts
with its family word (`lsp_navigation.go`, `git_sync.go`). Name a file after
its main type or concept; never make a catch-all file such as `util.go`.

<a id="file-platform"></a>
### FILE-PLATFORM: platform variants end with the platform

The go command reads a `_GOOS`, `_GOARCH` or `_GOOS_GOARCH` suffix as a build
constraint, but only after an underscore. Name variants of one concept with
one prefix and the platform last: `backend_linux.go`, `backend_windows.go`,
`backend_other.go`. A fallback ends in `_other`, `_unix`, `_bsd`, `_posix`,
`_cgo` or `_nocgo` and carries an explicit `//go:build` line. Never end a file
that is not platform code with a platform word such as `_js` or `_arm`.

<a id="file-stray"></a>
### FILE-STRAY: only sources are tracked

Delete editor and tool copies such as `*.go.<digits>`, `*.orig` or `*~`.

<a id="doc-package"></a>
### DOC-PACKAGE: one package comment per package

Write `// Package name ...` (or `// Command name ...` for a program) directly
above the package clause, in one file only.

<a id="doc-entry"></a>
### DOC-ENTRY: the comment lives in the entry file

A package with three or more files keeps its package comment in the file
named after the package (`http.go`) or in a `doc.go` that holds only the
comment and the package clause.

## Exported names

<a id="name-repeat"></a>
### NAME-REPEAT: do not repeat the package name

Callers already write the package name, so `bufio.Reader`, not
`bufio.BufReader`; `plan.Status`, not `plan.PlanStatus`. A type may share the
package name (`ring.Ring`, `setting.Settings`).

<a id="name-getter"></a>
### NAME-GETTER: no Get prefix

`Owner()`, not `GetOwner()`. Use a verb that says the call can block or fail
(`Fetch`, `Load`, `Lookup`) when it does. Keep "get" only when the concept is
a get, such as MCP `prompts/get`, and record it in `allow.txt`.

<a id="name-alias"></a>
### NAME-ALIAS: one type, one name

A type alias exists only while a type moves between packages and is removed in
the same change. Import the owning package instead of re-exporting its types.

## Rules enforced by golangci-lint

| Rule | Check |
|---|---|
| MixedCaps; initialisms in one case (`URL`, `ID`, `appID`) | staticcheck ST1003, revive `var-naming` |
| Receivers: one or two letters, the same in every method, never `this` or `self` | staticcheck ST1006 and ST1016, revive `receiver-naming` |
| Error strings lowercase without final punctuation; error variables `ErrX` | staticcheck ST1005 and ST1012 |
| Import aliases only to avoid a collision, lowercase | revive `import-alias-naming` and `redundant-import-alias` |
| Every new exported name has a doc comment that starts with its name | revive `exported`, on code added since the upstream branch (`make lint-new`) |

## Rules for review

A machine cannot judge these; reviewers do:

- A package name says what callers get from it, and a split follows subjects.
- A local name is as long as its scope needs: `i` in a short loop, a word or
  two in a long function. Omit type words (`users`, not `userSlice`).
- One-method interfaces are named for the method plus `-er` (`Reader`).
- A file holds one subject, so a maintainer can tell which file has something.

## Sources

- [Effective Go: names](https://go.dev/doc/effective_go#names) and
  [package names](https://go.dev/blog/package-names).
- [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments).
- [Google Go style: decisions](https://google.github.io/styleguide/go/decisions#naming)
  and [best practices](https://google.github.io/styleguide/go/best-practices#package-size).
- [Organizing a Go module](https://go.dev/doc/modules/layout),
  [build constraints](https://pkg.go.dev/cmd/go#hdr-Build_constraints) and
  [internal packages](https://pkg.go.dev/cmd/go#hdr-Internal_packages).
- Layouts of Go `src`, Kubernetes, Terraform, gopls and Prometheus: group
  folders with no files, package folders whose children are their parts, and
  platform variants that share one prefix.
