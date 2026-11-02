# Code-Intelligence Catalog

> Status: Current reference. The catalog package and its importer exist. The
> product does not read the catalog yet; the cutover is slice 5 of the
> [thin assembly path](../plans/autonomous-code-intelligence.md#current-checkpoint).

The catalog is the one owner of knowledge about languages, language servers and
installable tools. Product Go code asks the catalog. It does not name a
language, a server or a tool itself.

## Contents

`insight/catalog` holds three kinds of entries. Each entry lists its sources.

| Entry | What it says |
|---|---|
| Language | One LSP language identity: the files it covers (extensions, file names and patterns), its Linguist names, its `languageId`, root markers, workspace root markers, and its servers in order of preference. |
| Server | How to start a language server: command, arguments, environment, settings, initialization options, the package that provides the command, resource hints, and the declared diagnostic scope. |
| Package | How to install a tool: package URL, license, languages, and release assets for each platform. |

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
  supplies languages, file types, root markers and server launch data.
- `insight/catalog/authored.json`, written by Crude. It holds facts that no
  upstream source has: resource hints, diagnostic scope, and the earlier Crude
  launch and root rules. Every entry needs a note that says why it exists.
- Linguist data through go-enry. The importer assigns Linguist names by voting
  over each entry's extensions and file names, and adds the script
  interpreters that Linguist knows for them.

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
- `Load` adds packs in the same format. A pack entry replaces the entry with
  the same identity. The base catalog does not change. Packs are validated:
  identifiers, extensions, root markers (file names or patterns only, no
  paths), environment names, diagnostic scopes, package URLs and every
  reference between entries.
- `LanguageForPath` checks exact file names and file name patterns first,
  then the longest extension, then the interpreter in a `#!` first line of the
  optional file head. When several languages claim one file (five extensions
  today, for example `.pl` and `.v`), the first by catalog ID wins. Content
  rules that separate them are later work. The measured cost is under 1
  microsecond for each path.
- `Server.WithHints` fills resource defaults. Explicit values always win.
- `Package.AssetFor` selects the release asset for the first matching
  platform name from `CurrentTargets`.
- `Names` lists every identifying string. The planned names gate uses it to
  keep these strings out of product Go code.

Every returned value is a copy. Callers cannot change the catalog.

## Licenses

The embedded data contains facts from the mason registry (Apache-2.0), Helix
(MPL-2.0) and Linguist through go-enry (MIT data, Apache-2.0 library). The
catalog records the license of each source. See `insight/catalog/NOTICE`.
