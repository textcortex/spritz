FROM ubuntu:24.04

# Example Spritz base image (agnostic). Build with context at images/
# so shared scripts are available (e.g. docker build -f examples/base/Dockerfile .).
# - SPRITZ_REPO_URL, SPRITZ_REPO_DIR, SPRITZ_REPO_BRANCH
# - SPRITZ_BOOTSTRAP_CMD, SPRITZ_BOOTSTRAP_MARKER

ENV DEBIAN_FRONTEND=noninteractive

ARG ZMX_VERSION=0.1.1
ARG ZMX_SHA256=""
ARG ZMX_BASE_URL="https://zmx.sh/a"
ARG TARGETARCH

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    openssh-client \
  && rm -rf /var/lib/apt/lists/*

COPY scripts/install-zmx.sh /tmp/install-zmx.sh
RUN ZMX_VERSION=$ZMX_VERSION ZMX_SHA256=$ZMX_SHA256 ZMX_BASE_URL=$ZMX_BASE_URL TARGETARCH=$TARGETARCH \
  bash /tmp/install-zmx.sh \
  && rm /tmp/install-zmx.sh

RUN useradd -m -s /bin/bash dev
USER dev

WORKDIR /workspace

COPY --chown=dev:dev examples/base/entrypoint.sh /usr/local/bin/spritz-entrypoint

ENTRYPOINT ["/usr/local/bin/spritz-entrypoint"]
CMD ["sleep", "infinity"]
