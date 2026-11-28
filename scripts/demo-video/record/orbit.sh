#!/bin/bash
# orbit.sh [DIR]: creates the demo project "orbit" as a fresh Git repository.
# orbit.sh --archive [DIR]: adds the launch archive, the localized errors and
# the load-test chart, as committed after the agent's run in the video.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
data="$here/../testdata"
git_demo() { git -c user.name="Crude Demo" -c user.email="demo@crude.invalid" "$@"; }

if [ "${1:-}" = "--archive" ]; then
  dir="${2:-/c/dev/orbit}"
  mkdir -p "$dir/internal/api" "$dir/docs" "$dir/scripts"
  cp "$data/orbit-archive/history.go" "$data/orbit-archive/messages.go" "$dir/internal/api/"
  git_demo -C "$dir" add internal/api/history.go internal/api/messages.go
  git_demo -C "$dir" commit -q -m "Add the launch archive and localized errors"
  cp "$data/orbit-archive/load.png" "$dir/docs/load.png"
  cp "$data/orbit-archive/plot_load.py" "$dir/scripts/plot_load.py"
  git_demo -C "$dir" add docs/load.png scripts/plot_load.py
  git_demo -C "$dir" commit -q -m "Plot the limiter under load"
  exit 0
fi

dir="${1:-/c/dev/orbit}"
if [ -e "$dir" ]; then
  echo "orbit.sh: $dir already exists; choose another folder or move it away first." >&2
  exit 1
fi
mkdir -p "$dir"
cp -r "$data/orbit/." "$dir/"
git_demo -C "$dir" init -q -b master
git_demo -C "$dir" add -A
git_demo -C "$dir" commit -q -m "Serve the launch schedule"
echo "created $dir"
