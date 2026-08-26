# Codex support

remotevibe's flow depends on one specific capability:

> a coding agent, running on a machine I control, that registers itself with
> the vendor's backend so the vendor's phone app can attach to that exact
> session.

For Claude Code this is `claude --remote-control <name>`, and everything in
this repository is built around it.

## Status: unverified

Whether the Codex CLI exposes an equivalent has **not** been confirmed, and
this repository deliberately does not guess. Codex's published remote story is
cloud-hosted task execution, which is a different shape: the code runs on the
vendor's machine, not on your VPS, so cloning a private repo into a container
you own is not part of it.

`internal/agent/agent.go` therefore ships a `Codex` driver that refuses to
start and explains why. The interface around it is settled, so adding support
is a small, contained change rather than a redesign.

## What a contributor needs to do

1. Find the Codex CLI flag (if any) that starts a **local, interactive**
   session that becomes attachable from the Codex phone app. Check
   `codex --help` and the current CLI docs; do not infer it from cloud task
   commands.
2. Fill in `Codex.Command` to render that command line, and drop the error
   from `Codex.Supported`.
3. Add whatever credentials it needs to `Codex.Env`. Follow the Claude
   precedent: forward the variable **by name** from the daemon's environment so
   its value never lands in `docker inspect` output or the process table.
4. Install the Codex CLI in `image/Dockerfile`.
5. Enable the `codex` option in the agent segmented control in `web/app.js`.

Nothing else in the daemon is Claude-specific: the container entrypoint runs
whatever `RV_AGENT_CMD` it is given.

## If no such mode exists

Then Codex does not fit this flow, and the honest answer is to say so rather
than to ship a worse imitation of it. A tmux-over-SSH fallback would work on a
laptop but is miserable on a phone, which is the whole reason Remote Control is
the hinge of this design.
