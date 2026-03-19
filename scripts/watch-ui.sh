#!/usr/bin/env bash
set -euo pipefail

UI_DIR="$(cd "$(dirname "$0")/../ui" && pwd)"
CLUSTER_NAME="${KIND_CLUSTER:-spritz}"
IMAGE="spritz-ui:latest"
NAMESPACE="spritz-system"
DEPLOYMENT="spritz-ui"

echo "👀 Watching $UI_DIR for changes..."
echo "   Cluster: $CLUSTER_NAME | Image: $IMAGE"
echo "   Press Ctrl+C to stop"
echo ""

# Debounce: wait 2s after last change before triggering a build
fswatch -r -l 2 \
  --exclude '\.git' \
  --exclude 'node_modules' \
  --exclude 'dist' \
  --exclude '\.swp$' \
  "$UI_DIR/src" "$UI_DIR/index.html" "$UI_DIR/package.json" "$UI_DIR/vite.config.ts" \
  | while read -r _; do
      # Drain any queued events
      while read -r -t 0.1 _; do :; done

      echo ""
      echo "$(date '+%H:%M:%S') ── Change detected, rebuilding..."

      if docker build -t "$IMAGE" "$UI_DIR" --quiet; then
        echo "$(date '+%H:%M:%S') ── Loading image into kind..."
        kind load docker-image "$IMAGE" --name "$CLUSTER_NAME" 2>&1 | tail -1

        echo "$(date '+%H:%M:%S') ── Restarting deployment..."
        kubectl rollout restart "deployment/$DEPLOYMENT" -n "$NAMESPACE" > /dev/null
        kubectl rollout status "deployment/$DEPLOYMENT" -n "$NAMESPACE" --timeout=60s > /dev/null

        echo "$(date '+%H:%M:%S') ── ✅ Deployed successfully"
      else
        echo "$(date '+%H:%M:%S') ── ❌ Build failed"
      fi
    done
