FROM node:22-bookworm-slim

# Example Spritz image with OpenClaw preinstalled (agnostic).
# Build with context at images/ so shared scripts are available:
#   docker build -f examples/openclaw/Dockerfile .
#
# Runtime bootstrap env vars are handled by the shared spritz entrypoint:
# - SPRITZ_REPO_URL, SPRITZ_REPO_DIR, SPRITZ_REPO_BRANCH, SPRITZ_REPO_REVISION
# - SPRITZ_BOOTSTRAP_CMD, SPRITZ_BOOTSTRAP_MARKER

ENV DEBIAN_FRONTEND=noninteractive

ARG OPENCLAW_VERSION=latest

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    chromium \
    curl \
    fonts-liberation \
    git \
    libasound2 \
    libatk-bridge2.0-0 \
    libatk1.0-0 \
    libcups2 \
    libdbus-1-3 \
    libdrm2 \
    libgbm1 \
    libgtk-3-0 \
    libnspr4 \
    libnss3 \
    libxcomposite1 \
    libxdamage1 \
    libxfixes3 \
    libxkbcommon0 \
    libxrandr2 \
    nano \
    openssh-client \
    ripgrep \
    vim \
    xdg-utils \
  && rm -rf /var/lib/apt/lists/*

RUN npm install -g "openclaw@${OPENCLAW_VERSION}"

RUN useradd -m -s /bin/bash dev \
  && mkdir -p /home/dev/.openclaw \
  && chown -R dev:dev /home/dev/.openclaw

ENV PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/usr/bin/chromium
ENV XDG_CONFIG_HOME=/home/dev/.config

USER dev
WORKDIR /workspace

COPY --chown=dev:dev examples/base/entrypoint.sh /usr/local/bin/spritz-entrypoint
COPY --chown=dev:dev examples/openclaw/entrypoint.sh /usr/local/bin/spritz-openclaw-entrypoint

ENTRYPOINT ["/usr/local/bin/spritz-openclaw-entrypoint"]
CMD ["sleep", "infinity"]
