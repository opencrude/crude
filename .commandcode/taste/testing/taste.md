# Testing Taste

- For visual/interactive behavior, assert on PAINTED CELLS and prove with screenshots — tests asserting internal state have repeatedly passed while the feature was broken on screen; never claim success without actually running the tests. Confidence: 0.9
- Serious user-facing bugs (e.g., agent tab lifecycle) must be guarded by regression tests AND docs so they cannot regress again; tests and docs are mandatory parts of every fix, not afterthoughts. Confidence: 0.9
- Prefers real end-to-end verification (running the app, screenshots, scripted input) over golden-file ceremony ("forget golden bulshit stuff") — wants best-in-class behavior proven live. Confidence: 0.7
- Acceptance gates must actually pass before claiming done: gofmt, go build, focused tests, full suite, race, go vet, lint/design audit — paste real results. Confidence: 0.8
- Never dismiss a failing test as "pre-existing" without proving the cause; fact-check premises (line numbers drift) before acting. Confidence: 0.8
- For bug fixes, wants BOTH unit tests AND integration tests ("enough integration and unit tests so it never happens again") — coverage must guarantee non-regression, not just prove the one fix. Confidence: 0.8
