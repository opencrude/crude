# Demo video

These tools make [the README film](../../assets/crude-demo.mp4). It opens on a
staged zsh terminal on Linux (the previous command was `neofetch`), installs
Crude and starts it. From there the edit replays eight recorded Crude sessions
cell by cell. They follow one project, `orbit`: account and model setup, two
agents building a rate limiter up to the HTTP 429 test, review and commit,
hand editing, language servers, Errand setting up a Markdown server, the
Workspace panel's cost and disk use, a plot of the limiter under load and
right-to-left text.

The Crude screens are the same on Linux and Windows; the recordings were made
on Windows.

| Part | What it does |
|---|---|
| [`tools/castrec`](../../tools/castrec/doc.go) | Records a console program on Windows (ConPTY) as an asciicast v2 file, driven by a step script. |
| `record/` | Recording helpers: `record.sh` (one recording), `orbit.sh` (the demo project), `peek.sh` (print a screen), `shell.ps1` (the shell prompt), `profile.json` (a clean Crude profile), `seed-dashboard.py` (30 days of made-up use for the Workspace panel), `steps/` (scripted recordings). |
| `casts/*.cast.gz` | The recordings used by the video, with personal names removed. Pointer steps are stored as `i` events. |
| `player/` | `render.mjs` replays casts with `@xterm/headless`, paints each frame in headless Chrome and pipes PNG frames to ffmpeg. `story.js` is the edit: shots, pointer, camera inserts, captions, labels and sound cues. `engine.js` paints and relabels terminal cells; `fx.js` holds the staged scenes and the effects. |
| `music.mjs` | Synthesizes the soundtrack, keys, clicks and signal breaks from the cue sheet that `render.mjs --cues` writes. |
| `pack.mjs` | Removes personal names from a new recording and writes `casts/NAME.cast.gz`. |
| `dump.mjs`, `find.mjs`, `scan.mjs`, `cells.mjs` | Inspect casts: print a screen, find text positions, list when text is on screen, print one row's cells. |
| `testdata/` | The demo project `orbit`, and the files committed to it later in the video, including the chart and the script that plots it. |

## Render the video

Needs Node.js 22.12+, Chrome or Edge, ffmpeg and JetBrainsMono Nerd Font
(regular, bold, italic and bold italic) on Windows. The player loads the Crude
icon and the RTL fonts from the repository.

```bash
cd scripts/demo-video
npm ci
node --test replay.test.mjs
mkdir -p out
node player/render.mjs --cues out/cues.json --out out/video.mp4 \
  --crf 16 --preset medium --poster out/cover.png
node music.mjs out/cues.json out/music.wav
ffmpeg -i out/video.mp4 -i out/music.wav -map 0:v -map 1:a \
  -c:v libx264 -preset slower -crf 28 -tune animation -pix_fmt yuv420p \
  -af volume=-2.2dB -c:a aac -b:a 160k -movflags +faststart \
  -metadata title=Crude -metadata comment=7FFFFFFF out/crude-demo.mp4
```

The full render takes about three minutes. The last step keeps the film near
16 MB; the time-lapses change most of the screen on every frame. Review the
result before replacing `assets/crude-demo.mp4` and `assets/crude-demo.png`
with the new film and `out/cover.png`.

`node player/render.mjs --list` prints the shot list with film times,
`--stills 12.5,40 --dir out` renders single frames for review and
`--cast r2 --at complete+1 --dir out` renders a raw recording frame.

## Editing notes

Keep the whole terminal in view. A camera insert is a quick push to one
detail, a still hold long enough to read it, and a return to the full screen,
as in a surveillance film; the push lands with a short pixel snap. Inserts
mark the sub-agent, the 429, the commit message, the search results, the
completion menu, Errand's offer, the running server and the cost table.
Nothing else moves the camera.

The pointer is drawn from the recorded clicks and drags: it glides in, rings
on the click, rests and fades. Key caps in the subtitle name the shortcut on
screen. A time-lapse shows its speed at the right of the subtitle; waiting time
between steps is cut. Signal breaks (a few frames of split color and shifted
bands) mark some cuts, the model choice and the language change. They never
run as a texture.

The film labels the agent's model as `claude-opus-5-5` (`OPUS` in
`story.js`); the recordings ran on `deepseek-v4.1-flash:1017`. The model
picker shows the real Ollama Cloud choice between an Anthropic and an OpenAI
version of the same frame. The Workspace panel's 30 days of use come from
`record/seed-dashboard.py`, with invented models and prices. `neofetch`,
the HAL logo and the numbers in it are staged.

The references for these choices are [Mania Design on default UI treatments](https://www.mania.design/blog/spot-the-slop-a-ui-designers-guide-to-fixing-ai-defaults/),
[OpenArt on visual clichés](https://openart.ai/blog/visual-cliches-that-scream-made-by-ai/)
and [Nielsen Norman Group on motion](https://www.nngroup.com/articles/animation-purpose-ux/).
In practice: show the actual 429 result instead of a stats card, keep the
terminal's own light and colors, and let the selection draw the eye. No
numbered chapters, floating captions, decorative camera moves or flash frames.

## Record again

Recording needs Windows, Git Bash, PowerShell 7 and Go.

1. Create the demo project: `record/orbit.sh` (default `C:\dev\orbit`).
2. Record. `record/record.sh r1` replays `record/steps/r1.steps` (the first
   run: welcome, folder, trust, an Ollama Cloud account and its model; it
   types the API key from the `DEMO_SECRET` environment variable).
   `record/record.sh r9` replays the Arabic selection; run it with a
   `PROFILE_DIR` that already trusts `orbit` and has an account. The other
   recordings were driven by hand: `record/record.sh r2` starts Crude and
   waits. Write steps to `casts/r2.cast.inject` while it runs, watch with
   `record/peek.sh r2`, and create `casts/r2.done` to stop. Run
   `record/orbit.sh --archive` after the agent's run, as in the video. For
   the Workspace panel (`r8`), copy such a profile, run
   `python record/seed-dashboard.py PROFILE_DIR/.crude/workspaces/ID` and
   start Crude in it.
3. Pack the cast for the repository: `node pack.mjs r2`. The pack stops when
   a name or an API key is still in the cast. Shell suggestions from your own
   history can show other paths; remove them with
   `--also "TEXT=REPLACEMENT"` or start the shot after them.
4. Update the marks and times in `player/story.js`. Use `scan.mjs` to find
   when a screen appears and `find.mjs` for rows and columns.

Avoid combining marks such as Arabic tanween in text that the recording
selects or edits. The Windows console and the replay disagree about the cell
of a combining mark, so a partial redraw of that row lands one cell off and
shows a letter twice. `replay.test.mjs` checks the Arabic selection for it.

The recorder's console does not pass terminal image protocols through, so
Crude shows the image viewer's cell preview while recording. In a terminal
with Kitty graphics or Sixel, Crude shows the image's own pixels. The player
shows the chart the same way: it draws `testdata/orbit-archive/load.png` into
the preview's cells.
