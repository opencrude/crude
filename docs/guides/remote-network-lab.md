# Remote network lab

> Status: Current guide — Linux container acceptance for the Crude shell-to-core link.

The lab starts a real Crude shell in tmux. It connects through a routed link to
OpenSSH and a real Crude core in a separate container. The core uses a fixture
provider on its own side. A lost shell link does not stop the provider or hide
loss behind a local mock of the Crude protocol.

Run this gate in addition to [the focused QUIC gate](validation.md#protocol-generation-and-v2-acceptance).
Run it on a separate Docker host as well as the development box to check another
host's kernel and scheduling. Within either run, all containers share that
host's kernel. This is not evidence from two physical endpoints, a public
internet route, native Windows/macOS shells, or a real laptop sleep.

This is the transport foundation for the
[combined IDE workflow and network matrix](../plans/ide-workflow-acceptance.md#combined-workflow-and-network-acceptance).
The same link classes must drive the actual editor, language-server, agent,
terminal and other IDE workflows, with native temporal evidence. A pass here
does not fill those workflow cells.

## Prepare a host and run

Use Linux/amd64, rootful Docker, Python 3.11 or later and a kernel with `sch_netem`
and conntrack. Docker access is privileged. Use a host where you can create
internal bridges and namespaced traffic controls. No port is published. Only lab
containers receive the capabilities needed for namespaced routing and offload
configuration. Do not run the scripts with real Crude profiles or SSH keys.

Build the current product and its typed observer from the **same** source cohort
on a native build host:

```sh
lab=$(mktemp -d "${CRUDE_TEMP_DIR:-/tmp}/crude-netlab.XXXXXX")
bash scripts/build.sh -o "$lab/crude"
go build -o "$lab/observer" scripts/core_shell_observer.go
```

Build the lab image on the Docker host. Use an official Ubuntu 24.04 Base root
filesystem. Verify the Ubuntu archive signature and the tarball checksum before
importing it. The recipe was exercised with
[`ubuntu-base-24.04.5-base-amd64.tar.gz`](https://cdimage.ubuntu.com/ubuntu-base/releases/24.04/release/)
and its SHA-256 pin in [`netlab.py`](../../scripts/netlab/netlab.py). The image
build installs Ubuntu packages; the acceptance run itself neither pulls images
nor uses an external service.

```sh
docker import /path/to/verified/ubuntu-base-24.04.5-base-amd64.tar.gz crude-netlab-base:24.04
docker build --build-arg BASE=crude-netlab-base:24.04 -t crude-netlab:local - < scripts/netlab/Dockerfile
python3 -B scripts/netlab/netlab.py --image crude-netlab:local \
  --binary "$lab/crude" --observer "$lab/observer" --out "$lab/evidence" \
  --levels L0,L3,L8,L10,L13 --carriers ssh,quic,tls --parallel 3
```

For a separate Docker host, copy `scripts/netlab/`, `scripts/core_shell_acceptance.py`
and the two binaries while preserving the scripts' relative paths. Run the same
command on that host. Keep orchestration local to its engine so a slow SSH
control link does not change the applied fault schedule. Start long runs
detached (for example with `setsid nohup`) and poll for the result; a lost
control session stops an attached run and removes its resources.

Omit `--levels` to run the full ladder. `--repeat N` runs each case N times
in numbered directories (`L15-ssh-r3`); each repeat uses another netem seed
where seeding works. Use it for intermittent cases: a passing retry never
hides an earlier failure in the same run. The runner uses a new output directory
and exits nonzero on failure. `--seed` sets the netem random seed when supported
by the kernel and iproute2. Record unsupported seeding as such; identical seeds
do not replay the same TCP timings, packet schedule or host load. Start at low
parallelism for comparative latency measurements. Ctrl+C stops the exact run
and cleans its resources. `--keep` leaves that run's labeled resources for
inspection. Use the label in `manifest.json` to find them; never prune the host.

## Scenario ladder

The router translates a private service address between the two internal
bridges. It does not require a host forwarding rule or a change to the Docker
firewall. Its two egress queues shape opposite directions; their delays add.
Blackholes drop silently in the router's forwarding path. The NAT test changes
only the tested flow's mapping and checks the changed tuple. A 5-second fault
does not necessarily need a new transport: surviving the fault correctly is
also a pass. L9 injects an ICMP path probe during each late flap, which can
otherwise fall entirely between the shell's heartbeats after the turn ends.

| Level | Link or fault | What it exercises |
|---|---|---|
| L0 | Clean routed link | Reference attach and stream |
| L1–L3 | 30, 150 and 264 ms RTT; asymmetric throughput at L2 | Startup cost and interaction on regional, mobile and intercontinental links |
| L4–L6 | Independent loss, burst loss with jitter, periodic fades | Progress through short impairments while work continues |
| L7 | Established link, nominal 750 ms RTT and 3.5% loss each way | Sustained slow connection and correct RTT reporting |
| L8–L9 | 5-second outage, then a separate 30-second uplink cut and flaps | Recovery and queued input |
| L10–L11 | NAT port change; separate 120-second pause with link down | Path change and wake-up recovery |
| L12 | 29% loss each way for 60 seconds, then restoration | Exact delivery after severe loss |
| L13 | UDP blocked after the QUIC session starts | Resume over pinned TCP/TLS |
| L14 | 45-second outage with an unsent draft | Hold the draft through reconnect and submit it without retyping |
| L15 | Cold SSH with GEO-like delay, jitter and loss already present | System SSH authentication and full launcher/session startup on the slow link |
| L16 | Cold SSH at 1200 ms RTT without random loss | A late startup cleanup receipt must not discard an already attached workspace |

Each SSH, QUIC and TCP/TLS lane starts through strict SSH authentication.
The runner verifies the actual carrier in **Connection Details**. The TCP/TLS
lane blocks UDP before attach; L13 instead forces a live QUIC connection to
fall back. L15 and L16 are SSH-only. Friendly names do not assert what carried the run.

Parameter anchors include [QUIC Interop Runner](https://github.com/quic-interop/quic-interop-runner),
[Lighthouse network throttling](https://github.com/GoogleChrome/lighthouse/blob/main/docs/throttling.md#the-mobile-network-throttling-preset),
[Azure backbone RTT statistics](https://learn.microsoft.com/en-us/azure/networking/azure-network-latency),
the [Mile High WiFi measurements](https://doi.org/10.1145/3178876.3186057),
and the [netem manual](https://man7.org/linux/man-pages/man8/tc-netem.8.html).
The in-flight study observed a mean satellite RTT of about 750 ms and wide
loss variation. L7 uses that RTT with a controlled independent loss model; it
does not reproduce the study's loss distribution. L12 is a stress case near
QUIC Interop Runner's 30% loss profile, not a claim about normal access links.
One-way loss and cross-traffic matter. An ICMP average alone cannot certify
application responsiveness.

## Workflow lane

`--lane workflow` runs overlapping IDE work through the real UI on the steady
link classes (levels without their own fault schedule) and adds its own
outage. One case, in order:

1. Type a prompt in the composer and run it (a 20-token stream).
2. Start a 60-token agent stream. While it runs, open `README.md` from the
   file tree, type a line in the editor and save it. The server file must
   hold the exact bytes.
3. Block both directions for 8 seconds (30 seconds on cold GEO SSH). Type another line and save during
   the outage. The block must stop the save; after the link returns the save
   must complete with the exact bytes. Requiring a second explicit save fails the case.
   The longer cut must also prove a new SSH authentication; transport survival alone cannot pass it.
4. Return to the agent tab; the stream that ran under the editor must be exact.
5. The agent asks a question through the real `question` tool. Choose an
   answer and submit it in the deck; the provider must receive that answer once.
6. Open the terminal and run a command whose output can only come from the
   remote shell.

Typing uses `typeprobe.py` inside the client container: it sends one key at a
time at a human pace and captures the pane as fast as tmux answers (about every
5 ms), so per-key echo latency, lost keys and typed text that disappears after
it was visible are measured, not sampled at 5 Hz. The run fails when its own
sampling has a gap over 150 ms. Composer and editor echo is local on every link
class; the provisional budget is p95 ≤ 150 ms and max ≤ 400 ms until repeated
comparable runs calibrate it. Terminal input waits for the remote PTY
until the first confirmed PTY echo. At L3 and L7 the next printable keys must
meet the local echo budget. A hidden-input prompt must receive all keys without
showing any of them. A click waits until its painted target holds
still for 300 ms, as a person would. Earlier moves of that control are
recorded in `layout_shifts` and fail the case.

## Read the result and iterate

Start at `summary.json`, then open the failing case's `result.json` and
`final-a.txt`. Compare the measured profile, monotonic event times, carrier and
process/session identities to the manifest. The runner checks the live shell
view, provider executions and post-recovery turn: ordered output exactly once,
the original core and session, and a working new interaction after the fault.
For input faults it distinguishes an unsent draft from a prompt sent while the
link was down. A reconnected badge alone cannot pass a case.

Use the sampled frames, SSH logs and router counters to locate the difference
between a fault that was not applied, a transport that did not resume, and a
replica that did not catch up. Fix the owner, run focused regressions, rebuild
both binaries and rerun the failed case with comparable settings. Then rerun
the ladder. Compare cold attach, time to first visible output and recovery
separately. `stream_visibility` compares provider flush/finish to sampled
viewport times, so a fast fixture response does not hide a replica backlog.
The tmux samples are useful for finding multi-second changes, not
for proving a sub-frame input-latency SLO. A gate pass is bounded by its cases,
seeds, application journeys and executing hosts.

### Measured workspace delivery (2026-11-19)

L7 exposed a workspace-journal queue, not slow provider execution. The core
used to wait for an applied ACK between consecutive journal entries. With
several publications per round trip, completed output could sit behind older
updates. The sender now uses the [bounded ordered flight](../reference/core-client-protocol.md#push-publication-and-application-acks)
without changing when the shell ACKs a committed update.

This local Linux Docker comparison measures the fixture's end of output to
the first tmux frame containing the last token of the five-token follow-up:

| Carrier | Before | With bounded flight |
|---|---:|---:|
| SSH | 38.015 s | 0.597 s |
| QUIC | 36.304 s | 0.558 s |
| TCP/TLS | 44.802 s | 0.838 s |

The ICMP averages were 744–761 ms before and 762–785 ms after. Both versions
used the same L7 profile; this host's `tc` did not support a pinned netem seed.
These are per-carrier observations, not percentiles or a latency guarantee.

The 43 L0–L14 combinations passed on the development Docker host and on a
separate Linux Docker host. Cold SSH at L15 was intermittent on the
development host: Connection Details showed a connected shell, but its first
New Session click did not produce a root agent.

### Findings from the workflow lane (2026-11-20)

- **Lost New Session click (L15).** About two seconds after attach, the shell
  opens the workspace Aeron in the background. That request held the shell's
  one session slot for a round trip, and a New Session click in that window was
  dropped without a message. At 750 ms RTT with loss the window is seconds
  long. The click is now kept and runs when the Aeron reply arrives; a
  workspace switch waits instead of failing. A remote-journey regression
  reproduces the drop. After the fix, 6 of 6 cold L15/L16 runs passed on the
  development host and 12 of 12 on the separate host.
- **Question deck jump.** A two-choice question first painted 21 rows tall,
  then shrank to 10 rows about 250 ms later, so its choices moved under the
  pointer. The deck measured before it knew its width. Panel content now
  measures at the deck's real column (`ui.PreferredHeightComponent` takes the
  width), so the first frame has the final height.
- **Editor stuck after an outage at L7.** An edit sent during an 8-second
  outage never reached the core, and its call outlived its 30-second deadline
  while SSH reconnected. The shell looked up a receipt that did not exist,
  retried that lookup forever and showed "Changes not applied"; that dialog
  took the keyboard and the save never ran. The editor now sends the request
  again with its original identity: the core returns the recorded outcome, or
  admits it once if it never arrived. No dialog opens unless the core itself
  lost the outcome. At L7 the save made during the outage now completes
  12–19 seconds after the link returns, without a second save.

Development host, `--lane workflow`, one run per cell (seconds; echo in ms):

| Case | Open file | Save | Save after outage | Question shown | Answer to result | Composer/editor echo p95 | Terminal echo p95 |
|---|---:|---:|---:|---:|---:|---:|---:|
| L0 SSH / QUIC | 0.20 / 0.20 | 0.15 / 0.14 | 6.1 / 0.4 | 0.19 / 0.18 | 0.19 / 0.20 | 75–92 / 73–87 | 87 / 106 |
| L3 SSH / QUIC | 1.09 / 1.37 | 1.47 / 1.47 | 10.0 / 3.8 | 0.33 / 0.32 | 0.52 / 0.46 | 76–93 / 72–88 | 564 / 540 |
| L5 SSH / QUIC | 0.47 / 0.47 | 0.41 / 0.13 | 4.3 / 1.4 | 0.17 / 0.40 | 0.20 / 0.21 | 75–86 / 77–85 | 612 / 231 |
| L7 SSH / QUIC | 3.99 / 3.43 | 12.2 / 8.7 | 18.8 / 12.2 | 0.60 / 0.62 | 1.01 / 1.00 | 71–86 / 77–81 | 2902 / 4763 |

On the separate Docker host the same candidate passed the full stream ladder
(45 of 45) and the workflow lane on every steady level and carrier, twice each
(46 of 46), with no moved controls and no second save. Its editor echo p95 was
80–96 ms on every case. After the 8-second outage, QUIC resumed the save in
0.4–0.8 s on L0–L4, while SSH and TCP/TLS took 5–10 s to detect the dead
carrier and reconnect; one L7 TCP/TLS repeat took 42 s.

### Closing the startup, save and echo gaps

The cold-start case counts accepted authentications in the fixture sshd log
**at the trust prompt**, before any later reconnect. Startup and initial attach
must use one Crude session; enabling an OpenSSH control master is not a fix.

A core-side receipt trace on L7 SSH showed **five sequential edits before the
first save**. They arrived 1.9–2.5 s apart, even though the user had finished
typing. The save took 11.94 s from Ctrl+S to the observed file. Pending logical
edit groups now cross the link in one revision-checked transaction. Undo still
undoes individual groups. Named opens bring the first-frame EditorConfig back
with the document rather than taking a second round trip.

Terminal traces also corrected the initial diagnosis: some letters already
appeared in milliseconds. An older PTY frame cleared them, and shifted letters
did not enter prediction at all. Repeated requests for the same terminal size
could also arrive between the prompt and its first echo. The shell now separates
its requested size from the published PTY size, and reconciles matching echo
without installing speculative ink in the selection/copy source.

Final Linux measurements for production commit `ab9f33b9`. Each cell below
is the range across the two Docker hosts, one L7 workflow per carrier per
host (750 ms nominal RTT, 3.5% loss per direction, 1 Mbit/s). These are
observations, not guaranteed upper bounds.

| Carrier | Open to painted file | Ctrl+S to exact disk bytes | Confirmed-line echo p95 |
|---|---:|---:|---:|
| SSH | 1.60–2.10 s | 3.37–3.66 s | 74.7–79.1 ms |
| QUIC | 1.78–2.65 s | 2.48–2.54 s | 75.7–82.8 ms |
| TCP/TLS | 1.80–2.06 s | 3.59–4.63 s | 74.8–77.7 ms |

Two additional L15 runs started with exactly one SSH authentication and reached
the trust prompt in 22.2 and 26.5 seconds. During each run a 30-second two-way
cut forced another SSH authentication. The save requested while disconnected
reached the server without another keypress, 28.6 and 15.9 seconds after the
cut ended. Reconnect must budget time for an entire fresh SSH handshake, not
restart it every ten seconds while the session is still resumable.

An unshaped two-host smoke also used a local Linux shell and a private core
profile on the separate server over system SSH. The file opened in 0.77 s;
the saved bytes were verified independently on the server. This is separate
from the simulated profiles and is not a benchmark of all Internet routes.

### Two shells and a language server

`--lane peer --marksman /path/to/marksman` gives each shell a separate client
container, home, SSH key and router. It stages the supplied Linux x64 Marksman
binary in the private volume, records its digest, and exposes it on the core
host's PATH. The exercised version is `2026-02-08`; the ASCII Markdown fixture
uses .NET's invariant globalization mode. This tests an installed server, not
a language-specific installer. The test accepts trust for its isolated project
through the real dialog. In L7 both shells establish the requested carrier
before their routers switch to the measured GEO profile. A cold SSH fallback
is not evidence about an established TLS or QUIC connection.
One shell follows a definition while an agent runs. Only that shell loses its
link; the other edits and saves. The first catches up, edits again and the
second sees the unsaved change before saving it. Every check compares the
server file to independently expected bytes. Problems must display the broken
Markdown link. A two-file rename goes through review, Cancel, review again and
Apply. The peer must see it and Local History must list its safety snapshot.
Remote access settings must explain why the shell cannot change the host policy.

On a desktop lab host add `--native --parallel 1` to present the live client
through `scripts/tuishot.sh` at 160×48 and 84×26. Screenshots are distinct from
the timed tmux samples. Each capture closes before the next one starts.

### Verification record

The same product binary passed **91 cases on each of two Linux Docker hosts**:
45 stream and recovery cases, 23 editor/agent/terminal cases, and 23 two-shell
language-server cases. Each verdict uses a fresh project and checks the actual
carrier. An earlier second-client L7 check accidentally performed a cold dial
on an already impaired link: SSH correctly remained connected, but that run did
not test established TLS. Both peers now use the scenario's warm/cold schedule;
the corrected peer lane passed all 23 cases on both hosts. The physical SSH save,
two native window sizes, and local IPC restart/conflict journey are separate
checks, not additional cells in that count. Windows and macOS were not exercised
by this Linux campaign.
