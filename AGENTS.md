# AGENTS.md

Orientation for coding agents. Read `README.md` for what this project is and
`CONTRIBUTING.md` for the design constraints — neither is repeated here.

## Working here

- `go build ./...`, `go vet ./...`, `go test ./...`, `make image`. No external
  Go modules: `go.mod` has no `require` block, keep it that way.
- **`web/` is embedded into the binary** (`web/embed.go`). Editing the PWA and
  reloading does nothing until you `make build` and restart the daemon.
- New files in `web/` are invisible until added to the `//go:embed` list.

## Verifying without credentials

Neither the GitHub token nor the Claude login is available to you, and the
sign-in is interactive. Test the machinery around them instead — this is how
the current behaviour was checked:

```bash
# daemon: everything except /api/repos works with a junk token
RV_GITHUB_TOKEN=dummy RV_STATE_DIR=$PWD/state RV_ADDR=127.0.0.1:8799 ./bin/remotevibed

# container: a public repo and a stub in place of the agent
docker run -d --name rv-test --init \
  --label rv.session=1 --label rv.repo=octocat/Hello-World \
  --label rv.branch=master --label rv.agent=claude --label rv.name=hello-world \
  -e RV_REPO=octocat/Hello-World -e RV_AGENT=claude -e RV_AGENT_CMD='sleep 900' \
  remotevibe/agent:latest
```

The stub works because nothing is agent-specific below the driver: the
entrypoint runs whatever `RV_AGENT_CMD` it is handed, and the healthcheck only
asks whether the tmux pane is running something other than a shell. Killing the
stub is how you exercise the `error` path. Clean up test containers **and their
volumes** afterwards — leftovers show up as real sessions in the API.

## Authentication, if you touch it

A Claude login is two files: `.credentials.json` and, separately,
`.claude.json` (the account record). Persist only the first and the agent asks
to sign in again — which a phone-started container cannot answer. The image
sets `CLAUDE_CONFIG_DIR` so both land in one directory; keep them together.

`CLAUDE_CODE_OAUTH_TOKEN` is not a shortcut: a `claude setup-token` token does
not satisfy Remote Control, which asks for a browser sign-in anyway. The daemon
neither accepts a `token` auth mode nor forwards that variable into containers,
because an env token shadows a working profile. Do not reintroduce either.

`scripts/verify-remote-control.sh` is the check, and it needs the maintainer's
account. It has confirmed that a containerised `claude --remote-control`
session registers and appears in the iOS app; what it must be re-run for, twice,
is any change to how the profile reaches the container.
