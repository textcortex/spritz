#!/usr/bin/env bash
set -euo pipefail

repo_url="${SPRITZ_REPO_URL:-}"
repo_dir="${SPRITZ_REPO_DIR:-/workspace/repo}"
repo_branch="${SPRITZ_REPO_BRANCH:-}"
repo_revision="${SPRITZ_REPO_REVISION:-}"
repo_depth="${SPRITZ_REPO_DEPTH:-}"
repo_submodules="${SPRITZ_REPO_SUBMODULES:-}"
bootstrap_cmd="${SPRITZ_BOOTSTRAP_CMD:-}"
bootstrap_marker="${SPRITZ_BOOTSTRAP_MARKER:-${HOME}/.spritz/bootstrap.done}"

if [[ -n "$repo_url" && ! -d "${repo_dir}/.git" ]]; then
  mkdir -p "$(dirname "$repo_dir")"
  clone_args=()
  if [[ -n "$repo_depth" ]]; then
    clone_args+=(--depth "$repo_depth")
  fi
  if [[ "$repo_submodules" == "true" ]]; then
    clone_args+=(--recurse-submodules)
  fi
  git clone "${clone_args[@]}" "$repo_url" "$repo_dir"
  if [[ -n "$repo_branch" ]]; then
    git -C "$repo_dir" checkout "$repo_branch"
  fi
  if [[ -n "$repo_revision" ]]; then
    git -C "$repo_dir" checkout "$repo_revision"
  fi
fi

if [[ -n "$bootstrap_cmd" && ! -f "$bootstrap_marker" ]]; then
  mkdir -p "$(dirname "$bootstrap_marker")"
  (cd "$repo_dir" && bash -lc "$bootstrap_cmd")
  touch "$bootstrap_marker"
fi

if [[ "$#" -gt 0 ]]; then
  exec "$@"
fi

exec bash -l
