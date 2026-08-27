# remotevibe - vibing, on the go

https://private-user-images.githubusercontent.com/13247/642000880-748ce56b-6c42-444e-8ae6-a1ee32f0bcec.mp4?jwt=eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJnaXRodWIuY29tIiwiYXVkIjoicmF3LmdpdGh1YnVzZXJjb250ZW50LmNvbSIsImtleSI6ImtleTUiLCJleHAiOjE3ODc4MTUyMDksIm5iZiI6MTc4NzgxNDkwOSwicGF0aCI6Ii8xMzI0Ny82NDIwMDA4ODAtNzQ4Y2U1NmItNmM0Mi00NDRlLThhZTYtYTFlZTMyZjBiY2VjLm1wND9YLUFtei1BbGdvcml0aG09QVdTNC1ITUFDLVNIQTI1NiZYLUFtei1DcmVkZW50aWFsPUFLSUFWQ09EWUxTQTUzUFFLNFpBJTJGMjAyNjA4MjclMkZ1cy1lYXN0LTElMkZzMyUyRmF3czRfcmVxdWVzdCZYLUFtei1EYXRlPTIwMjYwODI3VDA3MTUwOVomWC1BbXotRXhwaXJlcz0zMDAmWC1BbXotU2lnbmF0dXJlPWQ0ODFkYjk4MjgwMzkzYWEwMjQ4MjJhZDc2MWRhMjgxZDlmNmQ2YjMzOGVmZTMzMDMyNzQ1OTY2OGQ0OWI3ZTUmWC1BbXotU2lnbmVkSGVhZGVycz1ob3N0JnJlc3BvbnNlLWNvbnRlbnQtdHlwZT12aWRlbyUyRm1wNCJ9.3aOuHSXYosj34-Ht7oFroRBX1xiotARKmZdpR1BSpoY

Start a coding session on your own hardware from your phone.

Open a small web app on your iPhone, pick one of your GitHub repos, tap start.
A container on your VPS clones the repo and launches Claude Code with Remote
Control enabled. Switch to the Claude app — the session is there, in your repo,
on your machine, ready to work.

```
iPhone ──tailnet──▶ remotevibed ──docker run──▶ container
 (PWA)                (Go, VPS)                  ├─ git clone <your repo>
                                                 └─ tmux: claude --remote-control
                                                              │
iPhone ◀───────────── Claude app ◀───── registers ────────────┘
```

No Anthropic API key: the agent signs in to your Claude subscription the same
way it does on your laptop. That sign-in is interactive and occasional — see
[Authentication](#authentication), which is the part of this project worth
reading carefully.

Sessions are long-lived: they survive the daemon restarting, keep their
checkout and their conversation across a container restart, and show up as
*error* with a reason — not a phantom "running" — when something goes wrong.

## Requirements

- A machine on your tailnet running Docker and Tailscale — a small VPS is fine
- A Claude subscription (Pro or Max)
- A fine-grained GitHub PAT

Nothing else: the daemon builds inside Docker, so the host needs no Go
toolchain.

## Setup

Everything below happens on the machine that will run the sessions. Put the
checkout wherever you keep services — `/opt/remotevibe` is a reasonable
default.

**1. Configure**

```bash
git clone https://github.com/alediaferia/remotevibe /opt/remotevibe
cd /opt/remotevibe && cp .env.example .env && chmod 600 .env
```

Edit `.env`: set `RV_GITHUB_TOKEN`, and set `RV_STATE_DIR` to a directory you
own (`/opt/remotevibe/state`). Leave `RV_AUTH_MODE=seeded`. Consider
`RV_SESSION_PREFIX=vps`, which is what makes sessions read as `vps/my-repo` in
the phone app instead of blurring together with the ones on your laptop, and
`RV_CPUS` / `RV_MEMORY`, so one session cannot take the box down.

Each `make` target is a one-line wrapper — `make image` is `docker build`,
`make auth` is `./scripts/bootstrap-auth.sh`, `make up` is
`docker compose up -d --build` — so a host without `make` can run the commands
directly.

**2. Sign in once**

```bash
make image     # the session container
make auth      # opens Claude in a container; use /login
make verify    # look for the session in the Claude app on your phone
make verify    # run it twice — a fresh profile must not ask you to log in
```

The second `make verify` is the one that matters: it starts from a blank
profile, exactly like a session your phone kicks off for a repo you have never
opened. If it reaches the session without prompting, the flow works. See
[Authentication](#authentication) for why this dance exists.

**3. Start the daemon**

```bash
make up        # docker compose up -d --build
make logs      # follow it
```

Your user needs to be in the `docker` group: the daemon runs in a container
with the Docker socket mounted, so it can start sessions as sibling containers.
`RV_STATE_DIR` is mounted at the same path inside the container as outside —
the daemon passes that path to the host's Docker when seeding a session, so it
has to mean the same thing on both sides.

**4. Put it on your tailnet**

```bash
tailscale serve --bg 8787
tailscale serve status     # the https URL to open on your phone
```

If that host already serves something on 443, put remotevibe on another HTTPS
port instead — `tailscale serve --bg --https=8443 8787`. Tailscale allows 443,
8443 and 10000. Do not use `--set-path`: the PWA fetches from absolute paths
and will not work under a subpath.

Open that URL in Safari on the iPhone and add it to the home screen — it is a
PWA, so it gets its own icon and no browser chrome. The daemon publishes only
to loopback; `tailscale serve` is what makes it reachable, and only from your
tailnet.

### Running it without compose

`make build && make run` runs the daemon straight from the checkout, which is
the convenient shape for hacking on remotevibe itself.

For a host-native service instead of a container, `deploy/` has a systemd unit
and `deploy/install.sh`, which installs to `/usr/local/bin` with an
`/etc/remotevibe.env` and a dedicated service user. Compose is the path that
gets exercised, so prefer it unless you have a reason not to; with the systemd
route, remember that the interactive `make auth` has to run as the service user,
since it writes the profile that user reads.

### GitHub token

One fine-grained PAT does all three jobs: listing your repos in the UI, cloning
inside the container, and pushing the agent's commits back. It needs
**Contents: read & write** and **Metadata: read** on the repositories you care
about. There is no OAuth device flow, on purpose — this is a single-user tool.

## Authentication

The agent needs your Claude credentials inside the container, and Anthropic's
sign-in is interactive. A session you start from your phone has nobody at a
keyboard to answer that prompt, so the whole question is how an
**already-authenticated profile** reaches a brand new container.

One thing to know before choosing a mode: a login has two halves. The
credentials live in `.credentials.json`, but the account record — which
subscription, which user — lives in a *separate* file, `.claude.json`, that
sits outside the credentials directory. Persist only the first and the agent
asks you to sign in again, credentials or not. The image sets
`CLAUDE_CONFIG_DIR` so both halves land in one directory, which is what makes
any of this survive a restart.

### `RV_AUTH_MODE=seeded` (default)

`make auth` signs you in once and leaves the profile on the host in
`$RV_STATE_DIR/agent-home`. Every new session mounts that directory read-only
and **copies** it into its own volume on first start.

- one sign-in, ever; new repos start silently, which is what the phone flow needs
- sessions keep their own credentials, conversation and project state
- if a token refresh in a session makes the canonical copy stale, re-run
  `make auth`

### `RV_AUTH_MODE=shared-home`

Every session mounts that same profile directly instead of copying it.

- nothing to re-seed, and a refresh in one session benefits all of them
- but concurrent sessions write to one config file and one session history

### There is no token mode

`claude setup-token` looks like the obvious fit — a long-lived credential, no
files to sync — and it was the original default here. It does not work: the
token carries no account record, and a Remote Control session stops and asks
for a full browser sign-in with different scopes regardless. Setting
`RV_AUTH_MODE=token` now fails at startup with that explanation rather than
wasting your time.

### If a session never appears on your phone

Open the session's **Logs** — the agent pane is included, and it shows exactly
what the session is sitting on. The two answers you are most likely to see:

- *a sign-in prompt* — the profile did not reach the container; re-run
  `make auth` and check `$RV_STATE_DIR/agent-home` holds both
  `.credentials.json` and `.claude.json`
- *a dialog awaiting a keypress* — one the pre-seeded profile did not cover.
  You can unstick it by hand:

```bash
docker exec -it rv-<owner>-<repo> tmux attach -t agent
```

`make auth` runs in the same permission mode your sessions use, so any warning
you accept during it is recorded in the profile they are seeded from. Accepting
one inside a throwaway `make verify` container does **not** carry over — that
container is deleted, and with it the copy of the profile you just changed.

### Refreshing

Sessions fail with an auth error in the phone app when the credential lapses,
and a session that stalls on the sign-in prompt shows up as *error* with that
reason rather than as a session your phone will never find. Re-run `make auth`,
then stop and start the affected sessions. `GET /healthz` reports the
configured mode.

## How it works

**Docker is the registry.** Every session container carries `rv.*` labels
(repo, branch, agent, name, created). The daemon lists sessions by querying
Docker, so restarting it — or rebooting the VPS — never loses track of
anything, and there is no state file to drift out of sync.

**Three lifetimes, kept separate.** The *container* is the per-repo sandbox and
restarts with `unless-stopped`. *tmux* is the durable TTY inside it, so the
agent survives anything happening to the daemon. *Remote Control* is the link
to your phone, and it is the only one of the three that talks to Anthropic.

**Starting is idempotent.** Session id, container name and volumes all derive
from the repo slug, so tapping a repo twice reattaches instead of spawning a
duplicate. (The slug flattens punctuation, so `owner/my.repo` and
`owner/my-repo` would collide — rename one if you own both.)

**Sessions survive restarts.** Each session gets two volumes: the checkout at
`/workspace`, and the agent's profile at `CLAUDE_CONFIG_DIR` — credentials,
account record, conversation and all. The second one is what makes a reboot
resume the session instead of quietly starting a blank one under the same name.
Stopping a session keeps both, so restarting it picks up the same branch, the
same uncommitted work and the same context; pass `?purge=1` to `DELETE` to
throw all of it away.

**A new session never asks you to log in.** On first start the entrypoint
copies the canonical profile into the session's own volume, so a repo you have
never opened starts already authenticated.

**Readiness is real.** The container's `HEALTHCHECK` reports healthy only while
the agent process is actually alive in tmux, which is what the phone UI shows
as *starting → running*. It is a liveness check, not a one-time flag: if the
agent dies, the session goes back to *error* rather than lying about being
attachable.

**Disk is visible, because keeping volumes is the default.** Every session card
shows what its checkout and profile occupy. A *Storage* panel adds the totals
and — the part the session list structurally cannot show — volumes whose
container is gone, left behind by a stop without a purge. Stopping a session
offers both shapes: **Stop** keeps the checkout so the session resumes, **Stop
& delete** reclaims it. Sizes come from one cached `docker system df -v` that
refreshes off the request path, so a slow scan never stalls the session list.

**A prompt is not progress.** The agent starts in an interactive TTY, and
anything that stops for a keypress — sign-in, workspace trust, the
bypass-permissions warning — looks exactly like a healthy process to anything
watching from outside. The entrypoint therefore classifies startup from the
pane and records one of three outcomes: registered, waiting for a sign-in, or
never confirmed. The last two are reported as errors with the fix in the
message, and the profile is pre-seeded to skip the known dialogs. If a session
is rescued by hand, the supervisor notices and it goes back to *running*.

**Logs show the part that matters.** The agent runs in a detached tmux pane, so
nothing it prints reaches `docker logs` — including the auth error you are
looking for. `/api/sessions/{id}/logs` returns the container startup output
*and* a capture of the agent pane.

**The agent layer is pluggable.** A driver contributes environment variable
names and one command line; the entrypoint runs whatever `RV_AGENT_CMD` it is
handed. See [docs/codex.md](docs/codex.md) for what a Codex driver would need,
and why one is not shipped.

## Permissions, and what you are agreeing to

The default is `--permission-mode bypassPermissions`. You cannot tap "approve"
on every tool call from a phone, so the container is the security boundary
instead of the prompt: the agent can run anything it likes inside its own
container, with a GitHub token that can push to your repositories.

That is the actual trade. If you would rather keep approvals, set
`RV_PERMISSION_MODE=acceptEdits` and answer prompts in the app. Scope the PAT
to the repositories you actually hack on from your phone, not to everything.

The container runs as an unprivileged user by default, but that user has
passwordless sudo — installing a missing build dependency from your phone is
otherwise impossible — so with `bypassPermissions` the agent can become root
*inside its own container* whenever it wants. What it does not get is the
Docker socket, `--privileged`, or anything of the host: the boundary is the
container, not the user inside it. CPU and memory caps come from `RV_CPUS` and
`RV_MEMORY`.

## Security model

- **The tailnet is the perimeter.** The daemon binds to localhost and is
  published by `tailscale serve`. There is no login screen because a
  single-user, tailnet-only service gains nothing from one — do not expose the
  port publicly.
- **Secrets travel by name.** The daemon forwards `RV_GITHUB_TOKEN` with
  `docker run -e NAME`, inheriting the value from its own environment, so it
  never appears in the process table. It *is* visible in `docker inspect`
  output for the running container, as environment variables always are. The
  Claude credential never travels as an environment variable at all — it is a
  file in the session's profile volume.
- **Nothing is baked into the image.** Tokens live in `.env` (gitignored) or
  `/etc/remotevibe.env` (0600).

## API

| Method | Path | |
|---|---|---|
| `GET` | `/api/repos?q=&refresh=1` | repos visible to the token, recent first |
| `GET` | `/api/agents` | drivers and whether each is usable |
| `GET` | `/api/sessions` | live sessions, derived from Docker |
| `POST` | `/api/sessions` | `{"repo":"owner/name","branch":"main","agent":"claude"}` |
| `DELETE` | `/api/sessions/{id}?purge=1` | stop; `purge` also drops the volumes |
| `GET` | `/api/storage` | total and reclaimable bytes, plus orphaned volumes |
| `DELETE` | `/api/volumes/{name}` | delete one orphaned `rv-ws-*` / `rv-home-*` volume |
| `GET` | `/api/sessions/{id}/logs?tail=200` | startup log + agent tmux pane, text/plain |
| `GET` | `/healthz` | docker reachable, image present, auth mode |

## Working inside a session

The PWA is a launcher, not a terminal — the real work happens in the Claude
app. When you do need the box itself:

```bash
docker exec -it rv-owner-repo tmux attach -t agent   # watch or drive the agent
docker exec -it rv-owner-repo bash                   # a plain shell
docker logs -f rv-owner-repo                         # clone + startup output
```

If the agent exits, its tmux pane drops back to a shell and the container stays
up, so you can relaunch it by hand instead of losing the checkout.

## Layout

```
cmd/remotevibed      daemon entrypoint
internal/config      environment-driven configuration
internal/dockerx     container lifecycle via the docker CLI
internal/ghclient    repository listing
internal/agent       agent drivers (claude, codex stub)
internal/httpapi     JSON API + static serving
web/                 the PWA, embedded into the binary
image/               session container: Dockerfile, entrypoint, healthcheck
deploy/              systemd unit + installer
scripts/             auth bootstrap, remote-control smoke test
```

The container image is meant to be forked: add your toolchains to
`image/Dockerfile` so the repos you clone are actually buildable from a phone.

## License

MIT
