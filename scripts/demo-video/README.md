# Demo video

These tools make [the README film](../../assets/crude-demo.mp4). After a staged
PowerShell install transcript, the edit replays five recorded Crude sessions
cell by cell. It follows one project, `orbit`, from a plain-language request to
the HTTP 429 test, its cost and a plot of the limiter under load.

| Part | What it does |
|---|---|
| [`tools/castrec`](../../tools/castrec/doc.go) | Records a console program on Windows (ConPTY) as an asciicast v2 file, driven by a step script. |
| `record/` | Recording helpers: `record.sh` (one recording), `orbit.sh` (the demo project), `peek.sh` (print a screen), `shell.ps1` (the shell prompt), `profile.json` (a clean Crude profile), `steps/` (scripted recordings). |
| `casts/*.cast.gz` | The recordings used by the video, with personal names removed. |
| `player/` | `render.mjs` replays casts with `@xterm/headless`, paints each frame in headless Chrome and pipes PNG frames to ffmpeg. `story.js` is the edit: shots, camera, captions and sound cues. `engine.js` paints terminal cells; `fx.js` holds the effects. |
| `music.mjs` | Synthesizes the soundtrack from the cue sheet that `render.mjs --cues` writes. |
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
  --crf 20 --preset medium --poster out/cover.png
node music.mjs out/cues.json out/music.wav
ffmpeg -i out/video.mp4 -i out/music.wav -map 0:v -map 1:a -c:v copy \
  -af volume=-2.2dB -c:a aac -b:a 160k -movflags +faststart \
  -metadata title=Crude -metadata comment=7FFFFFFF out/crude-demo.mp4
```

Review the result before replacing `assets/crude-demo.mp4` and
`assets/crude-demo.png` with the new film and `out/cover.png`.

`node player/render.mjs --stills 12.5,40 --dir out` renders single frames for
review; `--cast r2 --at complete+1 --dir out` renders a raw recording frame.

## Editing notes

Keep the whole terminal in view. Cut out waiting time instead of accelerating
pages of output. The only lens move is at the `time.` completion menu: establish
the page, move closer, hold, return. Subtitles sit below the footer, never over
the controls. The brief color split belongs to the language change, not to the
whole frame. Keep the opening quiet enough to hear the keys.

The references for this cut were [Mania Design on default UI treatments](https://www.mania.design/blog/spot-the-slop-a-ui-designers-guide-to-fixing-ai-defaults/),
[OpenArt on visual clichés](https://openart.ai/blog/visual-cliches-that-scream-made-by-ai/)
and [Nielsen Norman Group on motion](https://www.nngroup.com/articles/animation-purpose-ux/).
In practice: show the actual 429 result instead of a stats card, keep the
terminal's own light and colors, and let the selection draw the eye. No
numbered chapters, floating captions, decorative camera moves or flash frames.

## Record again

Recording needs Windows, Git Bash, PowerShell 7 and Go.

1. Create the demo project: `record/orbit.sh` (default `C:\dev\orbit`).
2. Record. `record/record.sh r1` replays `record/steps/r1.steps` (the first
   run: welcome, folder, trust and account setup; it types the API key from
   the `OR_KEY` environment variable). The other recordings were driven by
   hand: `record/record.sh r2` starts Crude and waits. Write steps to
   `casts/r2.cast.inject` while it runs, watch with `record/peek.sh r2`, and
   create `casts/r2.done` to stop. Run `record/orbit.sh --archive` after the
   agent's run, as in the video.
3. Pack the cast for the repository: `node pack.mjs r2`. The pack stops when
   a name or an API key is still in the cast. Shell suggestions from your own
   history can show other paths; remove them with
   `--also "TEXT=REPLACEMENT"` or start the shot after them.
4. Update the marks and times in `player/story.js`. Use `scan.mjs` to find
   when a screen appears and `find.mjs` for rows and columns.

The recorder's console does not pass terminal image protocols through, so
Crude shows the image viewer's cell preview while recording. In a terminal
with Kitty graphics or Sixel, Crude shows the image's own pixels. The player
shows the chart the same way: it draws `testdata/orbit-archive/load.png` into
the preview's cells.
