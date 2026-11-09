# User Language

> Status: Current reference. Every word that Crude shows to a user follows this
> contract: labels, status text, notices, dialogs, errors, hints, settings help
> and CLI messages.

Crude writes for a user who does not know how Crude works inside. Text tells the
user what is true, what changed and what to do next. It does not teach the
implementation.

## Style

Text follows ASD-STE100 (Simplified Technical English):

- Use short sentences. Procedural sentences have no more than 20 words.
  Descriptive sentences have no more than 25 words.
- Use the active voice. Use the imperative for an instruction: "Open
  Workspace settings", not "Workspace settings can be opened".
- Use one meaning for each word. Use common words: "start", not "admit";
  "turned off", not "disabled by policy".
- Write one topic in each sentence.
- Do not use contractions. Do not use em dashes in UI text.
- Use the exact name of the control the user must use, in the case it is
  shown: **Trust folder**, **Workspace settings**.

## What to say

1. **Say the state, then the next step.** A status that needs action names the
   action: "Language servers off. This folder is not trusted." with a
   **Trust folder** action, not "Diagnostics pending".
2. **Keep normal states quiet.** Do not label a healthy or expected state. Show
   nothing, or one short word. "Syntax shared" is an internal fact, not a user
   state.
3. **Never show internal vocabulary.** Do not show enum values, protocol terms,
   identifiers or lifecycle names: pending, unverified, partial/context
   unknown, grant, epoch, admission, publication, generation, hidden. Do not
   show a row of zero counters. If no data exists, say why in one sentence.
4. **Be honest without hedging words.** If Crude does not know, say "Not
   checked yet" or "No results yet", and say what makes results appear. Do not
   claim that a project is clean when no checker ran.
5. **Errors have three parts.** What failed, the reason the user can act on,
   and what to do: "Could not start gopls. The file `gopls` was not found.
   Install gopls or set its path in Language Tools settings." Put technical
   detail in an expandable detail or the log, not in the headline.
6. **One line first, detail on demand.** A status line, pill or header segment
   has one short phrase. Longer explanations go in a tooltip, details row or
   dialog the user opens.
7. **Reading surfaces are not debug dumps.** Diagnostic cards show the problem;
   completion and parameter help show the relevant documentation. Keep internal
   identities, version clocks and routine coverage commentary in structured
   evidence, not in the text. Grow a message card before requiring scrolling.
   When the available space cannot fit the message, show a scrollbar.

## When to ask

Crude asks at the moment a decision is needed, in a dialog that explains the
choice in plain words. It does not hide a required decision in Settings and
then report the result as an error.

- Ask once for each decision and remember the answer. Show where to change it
  later.
- Give each choice a short label that names its result: **Trust folder**,
  **Open in restricted mode**. Do not use Yes/No for a choice that has two
  results.
- The safe choice has the default focus. For a security decision, Esc uses the
  safe choice for now and does not remember it, so Crude asks again later.
- Workspace trust is the reference case. The
  [workspace trust settings](settings.md#workspace-analysis-trust) describe
  the prompt and its choices.

## One owner for each message

The owner of a state derives its user-facing status once. The editor header,
footer pills, Problems panel and Status Overview render that status. A surface
does not compose its own wording from raw fields, because two surfaces then
show two different stories.

## Review checklist

- Can a new user understand the text without reading the source code?
- Does each status that needs action name the action?
- Is any internal word, enum value or zero counter visible?
- Is the text no longer than needed? Remove words that do not change the
  meaning.
- Is the same state shown with the same words on every surface?
