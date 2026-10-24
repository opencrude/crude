#!/usr/bin/env bash
# Launch crude in a dedicated terminal window, drive it with keys/mouse, and
# screenshot after each step. Always kills the window on exit.
#
# Usage: scripts/tuishot.sh <outdir> [step ...]
#
# Steps:
#   key:<xdotool-key-spec>    key:ctrl+alt+p  key:Down  key:Escape  key:shift+Down
#   quit:<key-spec>          expected clean product exit; must be the final step
#   await-exit              wait for an autonomous journey's clean exit (last step)
#   record:<seconds>        lossless native video of this exact window (FFMPEG_BIN)
#   type:<literal text>       type:main.go
#   click:<col>,<row>[,<btn>] cell coords, btn 1=left 2=mid 3=right 4=whlup 5=whldn
#   move:<col>,<row>          move pointer only (hover test)
#   down:<col>,<row>          hold left button (stationary drag acceptance)
#   up:<col>,<row>            release left button at the final endpoint
#   drag:<c1>,<r1>,<c2>,<r2>  press, move, shot mid-drag, release, shot
#   dragburst:<c1>,<r1>,<c2>,<r2>,<steps> flood motion without sleeps
#   wheel:<col>,<row>,<n>     n wheel-down clicks (negative = up) at cell
#   hwheel:<col>,<row>,<n>    n wheel-right clicks (negative = left) at cell
#   sleep:<seconds>
#   shot[:label]              extra screenshot
#
# Env: COLS ROWS PAINT_WAIT STEP_WAIT FONT_SIZE CRUDE_ARGS CRUDE_CWD KEEP_OPEN
# TERMINAL=alacritty|kitty|tilix selects the actual emulator (default alacritty).
# CRUDE_BIN may select a prebuilt, deterministic visual acceptance test binary.
# An isolated CRUDE_HOME must be a real short directory, not a symlink to a
# long path: the core canonicalizes it before creating its Unix socket.
set -uo pipefail

OUTDIR="${1:?usage: tuishot.sh <outdir> [step ...]}"
shift || true

COLS="${COLS:-200}"
ROWS="${ROWS:-50}"
PAINT_WAIT="${PAINT_WAIT:-3}"
STEP_WAIT="${STEP_WAIT:-0.7}"
FONT_SIZE="${FONT_SIZE:-9}"
CRUDE_ARGS="${CRUDE_ARGS:-}"
KEEP_OPEN="${KEEP_OPEN:-0}"
TERMINAL="${TERMINAL:-alacritty}"
case "$TERMINAL" in alacritty|kitty|tilix) ;; *) echo "ERROR: unsupported capture terminal: $TERMINAL" >&2; exit 1 ;; esac
if [ -z "${TERM_NAME:-}" ]; then
  case "$TERMINAL" in alacritty) TERM_NAME=alacritty ;; kitty) TERM_NAME=xterm-kitty ;; tilix) TERM_NAME=xterm-256color ;; esac
fi

REPO="$(cd "$(dirname "$0")/.." && pwd)"
CRUDE_CWD="${CRUDE_CWD:-$REPO}"
# Evidence belongs to session scratch; only the socket-bound CRUDE_HOME needs
# a separate real short directory on Unix. Give each capture private logs.
WORK="$(mktemp -d "${CRUDE_TEMP_DIR:-/tmp}/crude-tuishot-XXXXXX")" || exit 1
BIN="${CRUDE_BIN:-$WORK/crude}"
LOG="$WORK/crude.log"
LAUNCH="$WORK/launch.sh"
mkdir -p "$WORK" "$OUTDIR"
: > "$LOG"

# Fail before opening a terminal when an isolated profile cannot host a core.
# The canonical socket address is bounded by the smallest supported Unix limit.
if [ -n "${CRUDE_HOME:-}" ]; then
  mkdir -p "$CRUDE_HOME/.crude" || exit 1
  DATA_DIR="$(cd "$CRUDE_HOME/.crude" && pwd -P)"
  SOCKET="$DATA_DIR/corewire/core.sock"
  if [ "${#SOCKET}" -gt 103 ]; then
    echo "ERROR: canonical core socket path exceeds 103 bytes: $SOCKET" >&2
    echo 'Use a real short profile directory (mktemp -d /tmp/crude-shot-XXXXXX); a symlink does not shorten the canonical path.' >&2
    exit 1
  fi
fi

IMPORT=/home/lenovo/tools/imagemagick-7.1.2-18/bin/import
export DISPLAY="${DISPLAY:-:0}"

WIN_ID=""
CAPTURE_PID=""
POINTER_DOWN=0
VIDEO_PID=""
cleanup() {
  [ "$POINTER_DOWN" = "1" ] && xdotool mouseup 1 2>/dev/null
  if [ -n "$VIDEO_PID" ]; then
    kill "$VIDEO_PID" 2>/dev/null || true
    wait "$VIDEO_PID" 2>/dev/null || true
  fi
  [ "$KEEP_OPEN" = "1" ] && return 0
  [ -n "$WIN_ID" ] && xdotool windowkill "$WIN_ID" 2>/dev/null
  if [ -n "$CAPTURE_PID" ]; then
    kill "$CAPTURE_PID" 2>/dev/null || true
    wait "$CAPTURE_PID" 2>/dev/null || true
  fi
  return 0
}
trap cleanup EXIT

if [ -n "${CRUDE_BIN:-}" ]; then
  [ -x "$BIN" ] || { echo "CRUDE_BIN is not executable"; exit 1; }
else
echo "== building crude =="
# Embedded terminal needs libghostty-vt (cgo). Source the pin env so
# CGO_ENABLED=1 even when the parent shell disabled it.
# shellcheck disable=SC1091
( cd "$REPO" && . scripts/libghostty_env.sh && go build -o "$BIN" . ) || { echo "BUILD FAILED"; exit 1; }
fi

# NO_COLOR is inherited from some parent shells (e.g. Cursor's, which also sets
# TERM=dumb). Bubble Tea's colorprofile maps NO_COLOR to Ascii and strips every
# colour, which silently invalidates screenshots as colour evidence. Clear it.
cat > "$LAUNCH" <<EOF
#!/bin/sh
unset NO_COLOR CLICOLOR NO_COLOUR
export COLORTERM=truecolor
export TERM=${TERM_NAME:-alacritty}
cd "$CRUDE_CWD" || exit 1
"$BIN" $CRUDE_ARGS 2>>"$LOG"
echo "CRUDE-EXITED rc=\$?" >>"$LOG"
sleep 900
EOF
chmod +x "$LAUNCH"

# Separate emulator processes never attach to a user's existing terminal. Borderless
# zero-padding grids keep pointer coordinates relative to the captured window.
case "$TERMINAL" in
alacritty)
alacritty --class CrudeShot,CrudeShot \
  -o "window.opacity=1.0" \
  -o "window.blur=false" \
  -o "window.decorations=\"none\"" \
  -o "window.dynamic_padding=false" \
  -o "window.padding.x=0" \
  -o "window.padding.y=0" \
  -o "window.dimensions.columns=$COLS" \
  -o "window.dimensions.lines=$ROWS" \
  -o "window.startup_mode=\"Windowed\"" \
  -o "font.size=$FONT_SIZE" \
  -o "cursor.style.blinking=\"Off\"" \
  -o "env.TERM=\"${TERM_NAME:-alacritty}\"" \
  -o "env.COLORTERM=\"truecolor\"" \
  -o "env.NO_COLOR=\"\"" \
  -e "$LAUNCH" >>"$WORK/terminal.log" 2>&1 &
;;
kitty)
kitty --class CrudeShot --name CrudeShot \
  -o remember_window_size=no -o "initial_window_width=${COLS}c" \
  -o "initial_window_height=${ROWS}c" -o "font_size=$FONT_SIZE" \
  -o window_padding_width=0 -o window_margin_width=0 \
  -o hide_window_decorations=yes -o background_opacity=1 \
  "$LAUNCH" >>"$WORK/terminal.log" 2>&1 &
;;
tilix)
# --new-process is essential: capture teardown must not own a user's Tilix server.
tilix --new-process --name=CrudeShot --class=CrudeShot --title=CrudeShot \
  --window-style=borderless --geometry="${COLS}x${ROWS}" \
  -e "$LAUNCH" >>"$WORK/terminal.log" 2>&1 &
;;
esac
CAPTURE_PID=$!

# Ignore an emulator's short-lived startup window. Resolve only visible windows
# owned by this dedicated process, then validate geometry before committing the
# exact target. Never fall back to a user window with a matching class.
GEOMETRY=""
for _ in $(seq 1 100); do
  while read -r candidate; do
    [ -n "$candidate" ] || continue
    owner=$(xdotool getwindowpid "$candidate" 2>/dev/null || true)
    [ "$owner" = "$CAPTURE_PID" ] || continue
    geometry=$(xdotool getwindowgeometry --shell "$candidate" 2>/dev/null) || continue
    [ -n "$geometry" ] || continue
    WIN_ID="$candidate"
    GEOMETRY="$geometry"
  done < <(xdotool search --onlyvisible --class "CrudeShot" 2>/dev/null || true)
  [ -n "$WIN_ID" ] && break
  kill -0 "$CAPTURE_PID" 2>/dev/null || { echo 'ERROR: dedicated capture terminal exited during startup'; tail -20 "$WORK/terminal.log"; exit 1; }
  sleep 0.15
done
[ -n "$WIN_ID" ] || { echo "ERROR: no window"; tail -20 "$WORK/terminal.log"; exit 1; }

# Pin to 0,0, raise above the desktop panel, and keep it there.
xdotool windowmove "$WIN_ID" 0 0 2>/dev/null
wmctrl -i -r "$WIN_ID" -b add,above 2>/dev/null
xdotool windowraise "$WIN_ID" 2>/dev/null
xdotool windowactivate --sync "$WIN_ID" 2>/dev/null
sleep 0.3

GEOMETRY=$(xdotool getwindowgeometry --shell "$WIN_ID" 2>/dev/null) || { echo 'ERROR: dedicated capture window disappeared before geometry'; exit 1; }
eval "$GEOMETRY"
CW=$((WIDTH / COLS))
CH=$((HEIGHT / ROWS))
echo "win=$WIN_ID geom=${WIDTH}x${HEIGHT}+${X}+${Y} cell=${CW}x${CH} grid=${COLS}x${ROWS}"
[ "$CW" -gt 0 ] && [ "$CH" -gt 0 ] || { echo "ERROR: bad cell size"; exit 1; }

# cell -> ABSOLUTE screen pixel at cell centre. xdotool mousemove uses screen
# coordinates, so the window origin must be added; otherwise clicks only
# land when the WM happens to place the window at (0,0) (the historical
# "clicks silently do nothing on some runs" flake).
px() { echo $(( X + $1 * CW + CW / 2 )); }
py() { echo $(( Y + $1 * CH + CH / 2 )); }

sleep "$PAINT_WAIT"

assert_running() {
  if grep -q '^CRUDE-EXITED ' "$LOG"; then
    echo "ERROR: Crude exited; refusing further input or screenshots" >&2
    tail -30 "$LOG" >&2
    exit 1
  fi
  if ! kill -0 "$CAPTURE_PID" 2>/dev/null; then
    echo "ERROR: capture terminal exited" >&2
    exit 1
  fi
}

N=0
shot() {
  assert_running
  N=$((N + 1))
  xdotool windowraise "$WIN_ID" 2>/dev/null
  local p
  p="$(printf '%s/%02d-%s.png' "$OUTDIR" "$N" "${1:-shot}")"
  if $IMPORT -window "$WIN_ID" "$p" 2>/dev/null; then echo "saved $p"; else echo "SHOT FAILED $p" >&2; exit 1; fi
}
slug() { printf '%s' "$1" | tr -c 'a-zA-Z0-9' '_' | cut -c1-22; }
# GTK/VTE intentionally ignores XSendEvent key injection. Activate and fence the
# exact dedicated window, then use XTEST, the same input route as a real key.
# Never send a key if focus changed to an unrelated desktop window.
focus_keys() {
  xdotool windowactivate --sync "$WIN_ID"
  [ "$(xdotool getactivewindow)" = "$WIN_ID" ] || { echo 'ERROR: capture window lost keyboard focus' >&2; exit 1; }
}
send_key() { focus_keys; xdotool key --clearmodifiers "$1"; }
await_exit() {
  for _ in $(seq 1 300); do
    grep -q '^CRUDE-EXITED ' "$LOG" && break
    sleep 0.1
  done
  grep -q '^CRUDE-EXITED rc=0$' "$LOG" || { echo 'ERROR: expected clean product exit was not observed' >&2; tail -30 "$LOG" >&2; exit 1; }
  echo 'verified expected clean product exit'
}

shot boot

for step in "$@"; do
  [ "$step" = await-exit ] || assert_running
  case "$step" in
    quit:*)
      [ "$step" = "${!#}" ] || { echo 'ERROR: quit must be the final capture step' >&2; exit 1; }
      spec="${step#quit:}"
      send_key "$spec"
      await_exit
      ;;
    await-exit)
      [ "$step" = "${!#}" ] || { echo 'ERROR: await-exit must be the final capture step' >&2; exit 1; }
      await_exit
      ;;
    record:*)
      duration="${step#record:}"
      [[ "$duration" =~ ^[0-9]+$ ]] && [ "$duration" -ge 1 ] && [ "$duration" -le 30 ] || { echo 'ERROR: record duration must be 1–30 seconds' >&2; exit 1; }
      ffmpeg="${FFMPEG_BIN:-ffmpeg}"
      command -v "$ffmpeg" >/dev/null || { echo 'ERROR: ffmpeg is required for exact-window video' >&2; exit 1; }
      video="$OUTDIR/native-window.mkv"
      [ ! -e "$video" ] || { echo 'ERROR: refusing to overwrite native video evidence' >&2; exit 1; }
      "$ffmpeg" -hide_banner -loglevel error -n -f x11grab -draw_mouse 0 -framerate 60 -window_id "$WIN_ID" -i "$DISPLAY" \
        -t "$duration" -c:v ffv1 -level 1 -threads 1 "$video" > "$OUTDIR/native-window-video.log" 2>&1 &
      VIDEO_PID=$!
      wait "$VIDEO_PID" || { VIDEO_PID=""; echo 'ERROR: native-window recording failed' >&2; tail -20 "$OUTDIR/native-window-video.log" >&2; exit 1; }
      VIDEO_PID=""
      echo "saved $video; native window=$WIN_ID, 60 Hz capture, review actual frame timestamps"
      ;;
    key:*)
      spec="${step#key:}"
      send_key "$spec"
      sleep "$STEP_WAIT"; shot "key-$(slug "$spec")" ;;
    type:*)
      txt="${step#type:}"
      focus_keys
      xdotool type --delay 45 -- "$txt"
      sleep "$STEP_WAIT"; shot "type-$(slug "$txt")" ;;
    click:*)
      IFS=',' read -r c r b <<<"${step#click:}"
      b="${b:-1}"
      xdotool mousemove --sync "$(px "$c")" "$(py "$r")" click "$b"
      sleep "$STEP_WAIT"; shot "click-${c}_${r}_${b}" ;;
    move:*)
      IFS=',' read -r c r <<<"${step#move:}"
      xdotool mousemove --sync "$(px "$c")" "$(py "$r")"
      sleep "$STEP_WAIT"; shot "move-${c}_${r}" ;;
    down:*)
      IFS=',' read -r c r <<<"${step#down:}"
      xdotool mousemove --sync "$(px "$c")" "$(py "$r")" mousedown 1
      POINTER_DOWN=1
      sleep "$STEP_WAIT"; shot "down-${c}_${r}" ;;
    up:*)
      IFS=',' read -r c r <<<"${step#up:}"
      # --sync waits for motion even when this is the already-held coordinate;
      # do not delay release until an unrelated desktop pointer event arrives.
      xdotool mousemove "$(px "$c")" "$(py "$r")" mouseup 1
      POINTER_DOWN=0
      sleep "$STEP_WAIT"; shot "up-${c}_${r}" ;;
    wheel:*)
      IFS=',' read -r c r n <<<"${step#wheel:}"
      btn=5; [ "${n:-1}" -lt 0 ] && { btn=4; n=$(( -n )); }
      xdotool mousemove --sync "$(px "$c")" "$(py "$r")"
      for _ in $(seq 1 "${n:-1}"); do xdotool click "$btn"; sleep 0.05; done
      sleep "$STEP_WAIT"; shot "wheel-${c}_${r}_${btn}" ;;
    hwheel:*)
      IFS=',' read -r c r n <<<"${step#hwheel:}"
      btn=7; [ "${n:-1}" -lt 0 ] && { btn=6; n=$(( -n )); }
      xdotool mousemove --sync "$(px "$c")" "$(py "$r")"
      for _ in $(seq 1 "${n:-1}"); do xdotool click "$btn"; sleep 0.03; done
      sleep "$STEP_WAIT"; shot "hwheel-${c}_${r}_${btn}" ;;
    drag:*)
      IFS=',' read -r c1 r1 c2 r2 <<<"${step#drag:}"
      xdotool mousemove --sync "$(px "$c1")" "$(py "$r1")" mousedown 1
      sleep 0.25
      xdotool mousemove --sync "$(px "$c2")" "$(py "$r2")"
      sleep 0.25; shot "dragheld-${c1}_${r1}-${c2}_${r2}"
      xdotool mouseup 1
      sleep "$STEP_WAIT"; shot "drag-${c1}_${r1}-${c2}_${r2}" ;;
    dragburst:*)
      IFS=',' read -r c1 r1 c2 r2 steps <<<"${step#dragburst:}"
      steps="${steps:-100}"
      xdotool mousemove --sync "$(px "$c1")" "$(py "$r1")" mousedown 1
      for i in $(seq 1 "$steps"); do
        c=$(( c1 + (c2-c1)*i/steps )); r=$(( r1 + (r2-r1)*i/steps ))
        xdotool mousemove "$(px "$c")" "$(py "$r")"
      done
      xdotool mouseup 1
      sleep "$STEP_WAIT"; shot "dragburst-${c1}_${r1}-${c2}_${r2}" ;;
    sleep:*) sleep "${step#sleep:}" ;;
    shot) shot extra ;;
    shot:*) shot "$(slug "${step#shot:}")" ;;
    *) echo "unknown step: $step" ;;
  esac
done

echo "== crude stderr tail =="
tail -30 "$LOG"
