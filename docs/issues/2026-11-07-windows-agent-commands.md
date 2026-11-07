# Windows agent commands lose their meaning (2026-11-07)

> Status: RESOLVED 2026-11-07. Contract:
> [background commands](../reference/background-commands.md#lifetime-and-termination).

## Reported

Agents on Windows often fail to run inline commands, in Crude and in other
agents. The user asked for a robust fix and whether a temporary command file
per command is a good method for speed and reactivity.

## Cause

Crude writes each Windows command to a temporary `.cmd` file because `cmd /C`
strips quotes unpredictably. A batch file parses the text differently from the
command line the model wrote:

- `%` is consumed: `%20` in a URL, `%H` in a format string, `%i` in a for loop.
- A line break ends the command even inside quotes. The rest of a multi-line
  `python -c "..."` then ran as separate commands, which is also unsafe.

## Fix

- `fileio.windowsBatchScript` gives the batch file the command-line meaning:
  `%NAME%` expands for defined variables, every other `%` is literal.
- A quoted argument that spans a line break is refused before anything runs,
  with a tool result that tells the model to use a script file.
- The environment prompt states the contract in one sentence.

## Measurements (this machine, 30 runs, median)

| Launch | Median | p90 |
|---|---:|---:|
| `cmd /C` inline | 43 ms | 46 ms |
| `cmd /C` temporary `.cmd` file (Crude) | 42 ms | 45 ms |
| Git Bash `bash -c` | 60 ms | 64 ms |
| Windows PowerShell 5.1 `-NoProfile -Command` | 327 ms | 341 ms |
| PowerShell 7 `-NoProfile -Command` | 490 ms | 514 ms |

The temporary file costs nothing measurable; process start dominates. The
fragility was the parse difference, not the file.

## Other agents and the decision

- Claude Code on Windows requires Git for Windows and runs commands with its
  real `bash.exe` (MSYS2), not an emulator, because models write bash best.
  Costs: a Git install, path and `/flag` translation by MSYS, no cmd built-ins.
- Codex CLI runs PowerShell on Windows. PowerShell starts 8–12 times slower
  here and has its own quoting rules.
- A Go POSIX interpreter (`mvdan.cc/sh`, used by go-task) runs bash syntax
  without bash, but Unix tools such as `grep` and `head` still do not exist.

Decision: keep cmd.exe as the one Windows contract (always present, fastest,
documented) with the translation above. Git Bash as an optional interpreter is
a separate product decision; it changes the prompt contract, path semantics
and tool warnings, and is not needed to make cmd.exe dependable.
