FROM node:22-bookworm-slim

# Example Spritz image with Claude Code and a long-lived ACP bridge.
# Build with context at images/ so shared scripts are available:
#   docker build -f examples/claude-code/Dockerfile .

ENV DEBIAN_FRONTEND=noninteractive

ARG CLAUDE_CODE_VERSION=2.1.72
ARG CLAUDE_AGENT_ACP_VERSION=0.21.0
ARG WS_VERSION=8.18.3

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    nano \
    openssh-client \
    ripgrep \
    vim \
  && rm -rf /var/lib/apt/lists/*

RUN npm install -g \
    "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
    "@zed-industries/claude-agent-acp@${CLAUDE_AGENT_ACP_VERSION}" \
    "ws@${WS_VERSION}" \
  && test -x /usr/local/bin/claude \
  && test -x /usr/local/bin/claude-agent-acp \
  && test -f /usr/local/lib/node_modules/ws/index.js

RUN useradd -m -s /bin/bash dev \
  && mkdir -p /home/dev/.claude /home/dev/.config \
  && chown -R dev:dev /home/dev/.claude /home/dev/.config

ENV XDG_CONFIG_HOME=/home/dev/.config

USER dev
WORKDIR /workspace

COPY --chown=dev:dev --chmod=0755 examples/base/entrypoint.sh /usr/local/bin/spritz-entrypoint
COPY --chown=dev:dev --chmod=0755 examples/claude-code/entrypoint.sh /usr/local/bin/spritz-claude-code-entrypoint
COPY --chown=dev:dev --chmod=0755 examples/claude-code/acp-server.mjs /usr/local/bin/spritz-claude-code-acp-server

ENTRYPOINT ["/usr/local/bin/spritz-claude-code-entrypoint"]
CMD ["sleep", "infinity"]
