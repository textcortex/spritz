FROM node:22-bookworm-slim

# Example Spritz image with Codex and a long-lived ACP bridge.
# Build with context at images/ so shared scripts are available:
#   docker build -f examples/codex/Dockerfile .

ENV DEBIAN_FRONTEND=noninteractive

ARG CODEX_VERSION=0.114.0
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
    "@openai/codex@${CODEX_VERSION}" \
    "ws@${WS_VERSION}" \
  && test -x /usr/local/bin/codex \
  && test -f /usr/local/lib/node_modules/ws/index.js \
  && test -f /usr/local/lib/node_modules/@openai/codex/package.json

RUN useradd -m -s /bin/bash dev \
  && mkdir -p /home/dev/.codex /home/dev/.config \
  && chown -R dev:dev /home/dev/.codex /home/dev/.config

ENV CODEX_HOME=/home/dev/.codex
ENV XDG_CONFIG_HOME=/home/dev/.config

USER dev
WORKDIR /workspace

COPY --chown=dev:dev --chmod=0755 examples/base/entrypoint.sh /usr/local/bin/spritz-entrypoint
COPY --chown=dev:dev --chmod=0755 examples/codex/entrypoint.sh /usr/local/bin/spritz-codex-entrypoint
COPY --chown=dev:dev --chmod=0755 examples/shared/spritz-acp-server.mjs /usr/local/shared/spritz-acp-server.mjs
COPY --chown=dev:dev --chmod=0755 examples/codex/acp-server.mjs /usr/local/bin/spritz-codex-acp-server

ENTRYPOINT ["/usr/local/bin/spritz-codex-entrypoint"]
CMD ["sleep", "infinity"]
