# Code-Intelligence Catalog

> Status: Current reference. The product reads the catalog for language
> detection, server launch, roots, status text, Settings, comment toggling,
> formatting and syntax grammars (slice 5 of the
> [thin assembly path](../plans/autonomous-code-intelligence.md#current-checkpoint)).

The catalog is the one owner of knowledge about languages, language servers and
installable tools. Product Go code asks the catalog. It does not name a
language, a server or a tool itself.

## Contents

`insight/catalog` holds three kinds of entries. Each entry lists its sources.

| Entry | What it says |
|---|---|
| Language | One LSP language identity: the files it covers (extensions, file names and patterns), its Linguist names, its `languageId`, root markers, workspace root markers, its servers in order of preference, and editor data: line comment tokens, block comment delimiters, the syntax grammar name, a formatter and declarative editing rules. A language without a server stays for its editor data. |
| Server | How to start a language server: command, arguments, environment, settings, initialization options, the package that provides the command, resource hints, and the declared diagnostic scope. |
| Package | How to install a tool: package URL, license, languages, and release assets for each platform. |

The formatter reads the document on standard input and writes the result on
standard output. In its arguments, `{file}` becomes the document path, so the
formatter can find its project configuration. It never gets write access to the
file. The grammar name selects a syntax grammar only when Crude links a grammar
of that name; linking a grammar is a dependency decision, not language data.

One language entry is one wire identity. For example, JavaScript, JSX,
TypeScript and TSX are four entries that share one server list.

The diagnostic scope is declared data, not a measurement. `project` means the
server documentation says it reports problems for files that are not open.
`open-files` means it reports only for open files. An empty value means unknown.

## Sources

`internal/catalogimport` generates `insight/catalog/catalog.json`. It is a
development tool and never runs in the product. Its inputs are pinned in
`internal/catalogimport/pins.json`:

- The mason registry release file `registry.json.zip`, checked against its
  SHA-256. It supplies packages, licenses and release assets.
- Helix `languages.toml` at one commit, checked against its SHA-256. It
  supplies languages, file types, root markers, server launch data, comment
  tokens, grammar names and formatters.
- `insight/catalog/authored.json`, written by Crude. It holds facts that no
  upstream source has: resource hints, diagnostic scope, and the earlier Crude
  launch and root rules. Every entry needs a note that says why it exists.
- Linguist data through go-enry. The importer assigns Linguist names by voting
  over each entry's extensions and file names, and adds the script
  interpreters that Linguist knows for them. When several entries claim one
  extension, the entry whose Linguist language has it as its primary
  extension keeps it. An uppercase Helix file type becomes the pattern `*.X`;
  the bare name `X` is kept only when Linguist knows such a file name.

go-enry runs only in the importer. Linking its classifier into Crude costs
about 9.6 MB of binary size and 20 ms and 16 MB of allocation at every process
start (measured 2026-11-02). The current catalog parse cost is recorded below.

The authored overlay also carries VS Code `brackets`, `onEnterRules` and
`indentationRules` for editor input. Patterns and optional flags retain their
ECMAScript meaning; the catalog validates actions, bounds and patterns and returns
detached data. Each imported configuration records its upstream file, commit and
SHA-256 in the authored note. Language configurations are data, not executable
extensions or Marketplace packages. Folding keeps its separate RE2 marker contract.

The importer keeps only declarative fields. It never runs code from a source.
It skips what Crude cannot use and lists each skip with `-v`.

To update the catalog, change the pins or `authored.json`, then run:

```bash
go run ./internal/catalogimport            # download, verify, write
go run ./internal/catalogimport -check     # fail when catalog.json is stale
```

## Behavior

- `Builtin` parses and validates the embedded catalog once, including editing
  patterns. Three Linux amd64 runs with the editing data measured 6.3–6.9 ms and
  3.74 MB of cumulative allocation, one time per process. This is not retained RSS.
- `Current` is what every product path reads: the embedded catalog plus the
  packs that `UsePacks` loaded. Each Crude process calls `UsePacks` once at
  start with `<data folder>/catalog`. Every `*.json` file there is a pack, in
  name order, at most 64. A pack that does not validate is skipped and the core
  logs it; the others still load. A pack is data only: a command it names
  still runs only through Trust and the normal launch path.
- `Load` adds packs in the same format. A pack entry replaces the entry with
  the same identity. The base catalog does not change. Packs are validated:
  identifiers, extensions, root markers (file names or patterns only, no
  paths), environment names, diagnostic scopes, package URLs and every
  reference between entries.
- `LanguageForPath` checks exact file names and file name patterns first,
  then the longest extension, then the interpreter in a `#!` first line of the
  optional file head. When several languages claim one file (five extensions
  today, for example `.inc` and `.v`), the first by catalog ID wins. Content
  rules that separate them are later work. A mixed six-path benchmark with
  detached editing data measured 1.2–1.6 microseconds, 818 B and 12 objects per
  lookup on Linux amd64. The editor caches compiled editing rules by catalog and
  path, so it does not pay this lookup/compile cost on each key.
- An explicit server path keeps the matching catalog server's defaults. The
  generic matcher compares executable basenames using the core host's filename
  rules. On Windows it ignores case and the native `.exe`, `.com`, `.cmd` and
  `.bat` suffixes, while retaining the exact configured path for launch. A
  different program name does not inherit another server's settings.
- `Server.WithHints` fills defaults. Explicit values always win. The authored
  gopls entry enables its experimental `semanticTokens` setting, which defaults
  off upstream, and supplies the same effective value at initialization and in
  `workspace/configuration`. An explicit startup or workspace choice takes
  precedence. No server-specific Go branch selects this behavior.
- `Package.AssetFor` selects the release asset for the first matching
  platform name from `CurrentTargets`.
- `Package.DownloadFor(command, targets)` is the install contract: the HTTPS
  release URL, file, archive kind (`zip`, `tar.gz`, `tar.xz`, `gz` or `raw`),
  program path and source of the program a server runs. Only release files on
  GitHub are supported. A package that a language package manager installs, a
  release file inside another archive, an archive kind the tool store cannot
  unpack, a program template Crude does not support, or an unsafe program path
  is not installable, and the second result says why in plain words
  (`TestEmbeddedServersResolveOrExplain`). The assembly plan uses it to mark a
  missing server as one Crude can install.
- `Package.PackageManagerFor(command)` describes supported package-manager
  metadata without running a program. The method data is in
  `insight/catalog/package_managers.json`. It preserves the selected binary,
  package version, supported qualifiers and Go subpath, and rejects ambiguous
  or unsafe forms. It does not verify a runtime, authorize an install or make
  the package installable. The setup review uses the description to name the
  package manager and runtime while that installation path remains open.
- `Names` lists every identifying string.

### Discovery and acquired tools

Project discovery uses these name/extension rules on a bounded, ignore-aware file
listing, not go-enry or project execution. Standard hidden/dependency/build
exclusions and inherited `.gitignore` rules limit the scope. Ignore links and
special files are not followed; actual read failures do not become a complete
plan. At 100,000 entries the plan is explicitly limited. Each detected language
retains a counted example path shown as **Example file** in Project Languages.
Ambiguous-extension content detection remains open; the example makes the current
name-based result inspectable.

`DownloadFor` describes a candidate; it grants no download or execution authority.
The native [project setup owner](settings.md#project-tool-setup) binds the exact
catalog/review to a one-shot human decision, stages the artifact and checks its
standard protocol initialization at an independent inert root. Successful joined
teardown precedes publication, current usage admission and assembly refresh.
A failed or uncertain new artifact never enters ordinary resolution. This thin
protocol check is not feature evidence or a compatibility guarantee.

## Names gate

`go run ./internal/namesgate` (part of `make design-audit`, and the test
`TestRepositoryNamesNoCatalogLanguage`) checks product Go syntax against the
built-in catalog. Distinctive extensions, dotted file names and root markers,
and server, command, package and binary names longer than three characters are
matched as exact string literals anywhere. Comparisons and switch cases with
language, grammar, server, tool, command, file, extension or interpreter
context also check all catalog names, including language IDs, Linguist names,
short names, bare filenames and interpreters. Identifier operands in these
branches are checked case-insensitively. A switch on `id` in a function with a
language-related type in its signature is checked too. Test files,
`insight/catalog`, `internal/catalogimport`, `internal/wiregen` and
`third_party` are not scanned.

This is a syntactic heuristic, not proof of the exit criterion. It cannot
resolve aliases or constants, constructed strings, arbitrary identifier names,
branches without recognizable context, or languages absent from the built-in
catalog. Single-letter identifiers and predeclared values are not checked as
identifiers. An unrelated use of a catalog name may also need review. In
particular, an unambiguous-looking name may have another meaning, and a bare
`"go"` outside a recognized branch is not checked. Review remains necessary.

`internal/namesgate/allow.txt` lists reviewed exceptions, one name in one file
per line, each with its reason. The linked native grammar bindings in
`syntax/registry.go` are explicitly excepted; the exception does not exempt the
package or other names. Other exceptions include Crude's own `.json` records,
safety and privacy rules, and the path heuristic in `jsonrepair`. An exception
that no longer matches fails the gate. The path-and-name scope covers every
occurrence of that name in the file, so additions in an excepted file still
need review.

Every returned value is a copy. Callers cannot change the catalog.

## Licenses

The embedded data contains facts from the mason registry (Apache-2.0), Helix
(MPL-2.0) and Linguist through go-enry (MIT data, Apache-2.0 library). The
catalog records the license of each source. See `insight/catalog/NOTICE`.
