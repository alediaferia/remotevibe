# The session container: a long-running box that holds one repository checkout
# and one agent process inside tmux.
#
# It is deliberately a general-purpose dev box rather than a minimal image --
# whatever you clone into it has to be buildable and testable from your phone.
# Add your own toolchains here; this is the file you are meant to fork.
FROM debian:bookworm-slim

ARG DEBIAN_FRONTEND=noninteractive
ARG NODE_MAJOR=22
ARG USERNAME=vibe
ARG UID=1000
ARG GID=1000

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl git gnupg jq less openssh-client procps ripgrep \
      sudo tmux unzip xz-utils python3 python3-pip build-essential \
    && rm -rf /var/lib/apt/lists/*

# Node: not required by the Claude Code native install, but most repositories
# want it and you cannot apt-get from a phone comfortably.
RUN curl -fsSL https://deb.nodesource.com/setup_${NODE_MAJOR}.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# GitHub CLI, so the agent can open PRs on your behalf.
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd -g ${GID} ${USERNAME} \
    && useradd -m -u ${UID} -g ${GID} -s /bin/bash ${USERNAME} \
    && echo "${USERNAME} ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/${USERNAME} \
    && mkdir -p /workspace && chown ${UID}:${GID} /workspace

USER ${USERNAME}
ENV HOME=/home/${USERNAME}
ENV PATH=${HOME}/.local/bin:${PATH}

# Claude Code, installed as the unprivileged user so it self-updates in place.
RUN curl -fsSL https://claude.ai/install.sh | bash \
    && claude --version

COPY --chown=${UID}:${GID} entrypoint.sh /usr/local/bin/rv-entrypoint
COPY --chown=${UID}:${GID} agent-health.sh /usr/local/bin/rv-health

WORKDIR /workspace

# "Healthy" means the agent is actually up and registered, not merely that the
# container is running. The daemon maps this straight to session status.
HEALTHCHECK --interval=10s --start-period=180s --timeout=5s --retries=3 \
  CMD /usr/local/bin/rv-health

ENTRYPOINT ["/usr/local/bin/rv-entrypoint"]
