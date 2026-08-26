# remotevibe

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

## Status

Written and reviewed; **not yet exercised end to end.** The Go daemon builds
and the container image is straightforward, but the VPS, Tailscale and systemd
paths are documented rather than tested, and one premise needs confirming on
your own account before you trust the rest:

> does a containerised `claude --remote-control` session appear in the iOS app?

`make verify` answers exactly that, in about a minute, without involving the
daemon. Run it first.

## Requirements

- A VPS on your tailnet, running Docker and Tailscale
- A Claude subscription (Pro or Max)
- A fine-grained GitHub PAT
- Go 1.24+ to build the daemon

## Setup

```bash
git clone https://github.com/alediaferia/remotevibe && cd remotevibe
cp .env.example .env      # fill in RV_GITHUB_TOKEN
make image                # build the session container
make auth                 # sign in once (interactive)
make verify               # check your phone — is the session listed?
make build && make run    # daemon on 127.0.0.1:8787
```

Then put it on the tailnet:

```bash
tailscale serve --bg 8787
tailscale serve status     # the https URL to open on your phone
```

Open that URL on the iPhone and add it to the home screen — it is a PWA, so it
gets its own icon and no browser chrome.

For a permanent install (systemd unit, service user, `/etc/remotevibe.env`):

```bash
sudo ./deploy/install.sh
```

### GitHub token

One fine-grained PAT does all three jobs: listing your repos in the UI, cloning
inside the container, and pushing the agent's commits back. It needs
**Contents: read & write** and **Metadata: read** on the repositories you care
about. There is no OAuth device flow, on purpose — this is a single-user tool.

## Authentication

The agent needs your Claude credentials inside the container, and Anthropic's
sign-in is interactive. There is no way around a manual first step; the choice
is *where the credential lives afterwards*.

### `RV_AUTH_MODE=token` (default)

`make auth` runs `claude setup-token` and prints a long-lived token. Put it in
your env file as `CLAUDE_CODE_OAUTH_TOKEN`; the daemon forwards it to every
container.

- containers stay stateless — no shared files, no refresh races
- when the token expires, `make auth` again and restart the daemon
- running sessions keep working on the credential they already have

### `RV_AUTH_MODE=shared-home`

`make auth` opens an interactive Claude session in a container whose `~/.claude`
is bind-mounted from `$RV_STATE_DIR/agent-home`. You `/login` once; every
session container mounts the same directory.

- nothing to re-paste: Claude refreshes the credential in place
- but all sessions share one config directory, so concurrent writes to
  `.claude.json` and session history are possible. Fine for one person with a
  couple of sessions; not something to lean on hard.

Start with `token`. If Remote Control turns out not to register with a
setup-token, switch to `shared-home` — `make verify` is how you find out, and
switching is a one-line change plus a re-run of `make auth`.

### Refreshing

Sessions fail with an auth error in the phone app when the credential lapses.
Re-run `make auth`, restart the daemon, and stop/start the affected sessions.
`GET /healthz` reports the configured mode.

## How it works

**Docker is the registry.** Every session container carries `rv.*` labels
(repo, branch, agent, name, created). The daemon lists sessions by querying
Docker, so restarting it — or rebooting the VPS — never loses track of
anything, and there is no state file to drift out of sync.

**Three lifetimes, kept separate.** The *container* is the per-repo sandbox and
restarts with `unless-stopped`. *tmux* is the durable TTY inside it, so the
agent survives anything happening to the daemon. *Remote Control* is the link
to your phone, and it is the only one of the three that talks to Anthropic.

**Starting is idempotent.** Session id, container name and workspace volume all
derive from the repo slug, so tapping a repo twice reattaches instead of
spawning a duplicate. Stopping a session keeps its workspace volume, so
restarting it resumes the same checkout, branch and uncommitted work; pass
`?purge=1` to `DELETE` to throw the checkout away too.

**Readiness is real.** The container's `HEALTHCHECK` goes healthy only once the
agent process is actually up, which is what the phone UI shows as
*starting → running* — not merely "the container exists".

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

The container runs as an unprivileged user, has no Docker socket, and gets
whatever CPU and memory caps you set in `RV_CPUS` / `RV_MEMORY`.

## Security model

- **The tailnet is the perimeter.** The daemon binds to localhost and is
  published by `tailscale serve`. There is no login screen because a
  single-user, tailnet-only service gains nothing from one — do not expose the
  port publicly.
- **Secrets travel by name.** The daemon forwards `RV_GITHUB_TOKEN` and
  `CLAUDE_CODE_OAUTH_TOKEN` with `docker run -e NAME`, inheriting values from
  its own environment, so they never appear in the process table. They *are*
  visible in `docker inspect` output for the running container, as environment
  variables always are.
- **Nothing is baked into the image.** Tokens live in `.env` (gitignored) or
  `/etc/remotevibe.env` (0600).

## API

| Method | Path | |
|---|---|---|
| `GET` | `/api/repos?q=&refresh=1` | repos visible to the token, recent first |
| `GET` | `/api/agents` | drivers and whether each is usable |
| `GET` | `/api/sessions` | live sessions, derived from Docker |
| `POST` | `/api/sessions` | `{"repo":"owner/name","branch":"main","agent":"claude"}` |
| `DELETE` | `/api/sessions/{id}?purge=1` | stop; `purge` also drops the workspace |
| `GET` | `/api/sessions/{id}/logs?tail=200` | container logs, text/plain |
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
