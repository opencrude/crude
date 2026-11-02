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
| Language | One LSP language identity: the files it covers (extensions, file names and patterns), its Linguist names, its `languageId`, root markers, workspace root markers, its servers in order of preference, and editor data: line comment tokens, block comment delimiters, the syntax grammar name and a formatter. A language without a server stays for its editor data. |
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
start (measured 2026-11-02). The catalog without it costs about 1.4 MB.

The importer keeps only declarative fields. It never runs code from a source.
It skips what Crude cannot use and lists each skip with `-v`.

To update the catalog, change the pins or `authored.json`, then run:

```bash
go run ./internal/catalogimport            # download, verify, write
go run ./internal/catalogimport -check     # fail when catalog.json is stale
```

## Behavior

- `Builtin` parses the embedded catalog once. The measured cost is about 4 ms
  and 1.4 MB of allocation, one time per process.
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
  rules that separate them are later work. The measured cost is under 1
  microsecond for each path.
- `Server.WithHints` fills resource defaults. Explicit values always win.
- `Package.AssetFor` selects the release asset for the first matching
  platform name from `CurrentTargets`.
- `Names` lists every identifying string.

## Names gate

`go run ./internal/namesgate` (part of `make design-audit`, and the test
`TestRepositoryNamesNoCatalogLanguage`) fails when a string literal in product
Go code is a name the catalog owns: a file extension, a dotted file name or
root marker, or a server, command, package or program name of four or more
letters. Language IDs, language names, bare-word file names, short program
names and script interpreters are not matched, because many are ordinary words
("json", "make", "C", "nil", "sh"). Test files, `insight/catalog`,
`internal/catalogimport`, `internal/wiregen` and `third_party` are not scanned.

`internal/namesgate/allow.txt` lists reviewed exceptions, one literal in one
file per line, each with its reason: Crude's own `.json` records, safety rules
that name shell startup files, privacy rules that name secret files, and the
path heuristic in `jsonrepair`. An exception that no longer matches fails the
gate. Language knowledge never belongs in that file: put it in the catalog.

Every returned value is a copy. Callers cannot change the catalog.

## Licenses

The embedded data contains facts from the mason registry (Apache-2.0), Helix
(MPL-2.0) and Linguist through go-enry (MIT data, Apache-2.0 library). The
catalog records the license of each source. See `insight/catalog/NOTICE`.
