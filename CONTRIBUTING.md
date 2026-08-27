# Contributing

remotevibe is small on purpose. Before adding anything, check it against the
shape of the tool: one person, one tailnet, one phone.

## Ground rules

- **No auth layer.** The tailnet is the perimeter. A login screen on a
  single-user private service is complexity without a threat model.
- **No database.** Docker labels are the session registry; if you need to store
  something new, put it in a label or ask whether you need it at all.
- **Stdlib only** in the daemon, **no build step** in `web/`. Both constraints
  keep deployment to "copy a binary".
- **Secrets by name, never by value.** Forward them with `docker run -e NAME`
  so they stay out of the process table.

## Adding an agent

Implement `agent.Driver` (see `internal/agent/agent.go`) and install the CLI in
`image/Dockerfile`. The container entrypoint runs whatever command the driver
renders, so nothing else should need to change. `docs/codex.md` walks through
the Codex case specifically.

## Before opening a PR

```bash
make fmt vet test build
make image
docker run --rm -v "$PWD:/mnt" -w /mnt koalaman/shellcheck:stable \
  scripts/*.sh image/*.sh deploy/install.sh   # no local install needed
```

If you touch anything in the start path, say in the PR whether you actually ran
a session end to end on real hardware — this repository tries to be honest
about what has and has not been exercised.
