# Workflow Taste

- Git destructive commands — reset, checkout, stash, revert, clean — are BANNED; never run them and always preserve the dirty worktree / uncommitted changes. Confidence: 0.95
- Do not stop until the whole plan is complete: no watering down, no deviation "in the name of practicality", no waiting for the user; work autonomously to completion. When the user green-lights a plan (e.g. "start the implementation"), kick off coding immediately using the plan's recommended defaults — don't hold up for answers to minor flagged design questions. Confidence: 0.9
- If genuinely stuck, a premise is false, or a design decision is ambiguous, STOP and report what is needed rather than improvising or weakening tests/behavior; the user explicitly asks the agent to surface its own confusion/ambiguity up front so logic issues can be resolved before coding. Confidence: 0.9
- Fact-check every premise against real code before acting; adapt when line numbers/details have drifted. Confidence: 0.8
- "Avoid myopic failure at all cost" — fixes must be holistic and consider related surfaces, state, and lifecycle (not narrow local patches); evaluate whether a refactor is genuinely worth it before doing it. Confidence: 0.9
- Cost-conscious delegation: use cheap task subagents for implementation and the expensive model only for thinking/reasoning; batch work, delegate whole chunks, and iterate prompts based on assessing subagent results. Confidence: 0.9
- Uses parallel subagents for independent fixes but serializes edits that could collide; always assesses subagent work and optimizes the next prompt. Confidence: 0.7
- Respect file ownership: edit only the files in scope, never touch unrelated services, and never revert existing changes — keep moving forward. Confidence: 0.8
- Secrets (API keys, OAuth tokens) must never live in agent profiles — agents only reference a provider; credentials stay in the app config layer and are stripped from workspace overlays. Confidence: 0.9
- If an install/dependency step fails due to a network error, tell the user and stop instead of nerfing around it. Confidence: 0.7
- Treats live user data as disposable ("data is temp... only code is important") — don't spend tokens/time preserving or migrating it. Confidence: 0.6
- Reads UI complaints precisely and expects exact differentiation between similar-looking elements (e.g., chat side rails vs scrollbar thumbs) — don't conflate them. Confidence: 0.7
- Plan/implementation artifacts should be written as markdown files in the working/project directory (repo root) so the user can review them — not tucked into a hidden config folder like `.commandcode/plans`; the user grants full write access and expects files written directly rather than asking permission. Confidence: 0.7
