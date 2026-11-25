"""Prepare locked dependencies outside the checkout, then run the official MCP referee offline."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
PINS = ROOT / 'scripts/mcpconformance'
REVISION = '2026-07-28'


def executable(name: str) -> str:
    path = shutil.which(name)
    if path is None:
        raise SystemExit(f'{name} was not found. Install Node.js and npm, then try again.')
    return path


def verify_completions(results: Path, full: bool) -> None:
    # The referee scores wire traffic. A driver can still reject its resulting
    # tool answer after the referee has observed every expected message.
    required = ['tools_call']
    if full:
        required += ['auth/basic-cimd', 'auth/scope-step-up', 'auth/pre-registration',
                     'sep-2322-client-request-state']
    for name in required:
        found = list(results.glob(f'{name}-*/stdout.txt'))
        if len(found) != 1 or found[0].read_text(encoding='utf-8').strip() != f'completed {name}':
            raise SystemExit(f'Crude did not verify the result for {name}.')


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('mode', choices=('prepare', 'quick', 'full'))
    ap.add_argument('directory', type=Path, help='new private tool directory outside the checkout')
    args = ap.parse_args()
    work = args.directory.resolve()
    if work.is_relative_to(ROOT):
        ap.error('Select a directory outside the checkout.')
    exe = 'crude-mcp-client' + ('.exe' if os.name == 'nt' else '')
    if args.mode == 'prepare':
        npm = executable('npm.cmd' if os.name == 'nt' else 'npm')
        work.mkdir(mode=0o700, parents=True, exist_ok=False)
        for name in ('package-lock.json', 'package.json'):
            shutil.copyfile(PINS / name, work / name)
        subprocess.run([npm, 'ci', '--ignore-scripts', '--no-audit', '--no-fund'], cwd=work, check=True)
        subprocess.run(['go', 'build', '-p=2', '-o', str(work / exe), './tools/mcpconformance'], cwd=ROOT, check=True)
        return
    for name in ('package.json', 'package-lock.json'):
        if (work / name).read_bytes() != (PINS / name).read_bytes():
            ap.error('Dependency pin changed. Prepare a new tool directory.')
    node = executable('node')
    results = Path(tempfile.mkdtemp(prefix='results-', dir=work))
    # The upstream CLI launches the Go client with a shell. The command is a
    # fixed relative filename. Paths, extra flags and account data are not interpolated.
    keep = {'PATH', 'SYSTEMROOT', 'COMSPEC', 'PATHEXT', 'WINDIR'}
    env = {k: v for k, v in os.environ.items() if k.upper() in keep}
    env.update({k: str(results) for k in ('TMP', 'TEMP', 'TMPDIR', 'HOME', 'USERPROFILE', 'CRUDE_HOME')})
    env['GOMAXPROCS'] = '2'
    cmd = [node, str(work / 'node_modules/@modelcontextprotocol/conformance/dist/index.js'),
           'client', '--command', ('' if os.name == 'nt' else './') + exe,
           '--output-dir', str(results), '--expected-failures', str(PINS / 'baseline.yml')]
    print(f'Official referee evidence: {results}', flush=True)
    profile = ['--requirements', REVISION] if args.mode == 'full' else ['--scenario', 'tools_call', '--spec-version', REVISION]
    result = subprocess.run(cmd + profile, cwd=work, env=env, timeout=120)
    # A missing executable, empty/skipped run, or failed client-side result
    # assertion must never pass just because the referee saw no failing frames.
    # The requirements manifest in this specific referee release contains 32
    # scored + 7 non-scored client scenarios. A skip with exit 0 is not a run.
    checks = list(results.glob('**/checks.json'))
    if len(checks) != (39 if args.mode == 'full' else 1):
        raise SystemExit(f'Incomplete referee evidence: {len(checks)} check files.')
    tool_dirs = list(results.glob('tools_call-*/checks.json'))
    if len(tool_dirs) != 1 or not any(c['id'] == 'tool-add-numbers' and c['status'] == 'SUCCESS'
            for c in json.loads(tool_dirs[0].read_text(encoding='utf-8'))):
        raise SystemExit('Missing successful tool invocation on the referee.')
    verify_completions(results, args.mode == 'full')
    raise SystemExit(result.returncode)


if __name__ == '__main__':
    main()
