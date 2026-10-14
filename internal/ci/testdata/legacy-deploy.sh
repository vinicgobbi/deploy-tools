#!/usr/bin/env bash
#
# Reference implementation of the forced command for the shared "deploy" SSH
# key; the Go deploy-ci must behave identically (see ci_test.go). sshd exposes the command the client actually
# asked for in $SSH_ORIGINAL_COMMAND; this script parses it and refuses
# anything it doesn't explicitly recognize, so a leaked CI key can only
# ever deploy/rollback/list under /var/www/<app> — never open a shell.
set -euo pipefail

APPS_ROOT="/var/www"
KEEP_RELEASES=3

die() { echo "error: $*" >&2; exit 1; }

# Only [a-zA-Z0-9_-], and must already exist under APPS_ROOT: prevents path
# traversal (../..) and creation of directories outside the convention.
validate_app() {
  local app="$1"
  [[ "$app" =~ ^[a-zA-Z0-9_-]+$ ]] || die "invalid app name: $app"
  [[ -d "$APPS_ROOT/$app" ]] || die "unknown app: $app (create $APPS_ROOT/$app first)"
}

validate_release_id() {
  local id="$1"
  [[ "$id" =~ ^[a-zA-Z0-9_.-]+$ ]] || die "invalid release id: $id"
  # The regex above allows "." and ".." (both are all-dots), which would
  # resolve releases/<id> to the releases dir or the app root itself —
  # a one-level path escape usable via `rollback <app> ..`.
  [[ "$id" != "." && "$id" != ".." ]] || die "invalid release id: $id"
}

prune_releases() {
  local app="$1"
  local releases_dir="$APPS_ROOT/$app/releases"
  local current_target
  current_target="$(readlink -f "$APPS_ROOT/$app/current" 2>/dev/null || true)"

  # Newest first; keep KEEP_RELEASES, always spare whatever "current" points to.
  local all_releases
  mapfile -t all_releases < <(ls -1t "$releases_dir")

  local kept=0
  for rel in "${all_releases[@]}"; do
    local full="$releases_dir/$rel"
    if [[ "$full" == "$current_target" ]]; then
      continue
    fi
    kept=$((kept + 1))
    if [[ $kept -ge $KEEP_RELEASES ]]; then
      echo "pruning old release: $app/$rel"
      rm -rf -- "$full"
    fi
  done
}

# Apps with persistent state (Laravel's .env, storage/) keep it in
# <app>/shared/, outside releases/. Every entry there gets symlinked into
# the fresh release, replacing whatever the tarball shipped in that slot
# (e.g. an empty storage/ stub from the git checkout).
link_shared() {
  local app="$1" release_dir="$2"
  local shared_dir="$APPS_ROOT/$app/shared"
  [[ -d "$shared_dir" ]] || return 0

  # dotglob: without it "*" skips dotfiles, so .env would never be linked.
  local entry name
  shopt -s dotglob
  for entry in "$shared_dir"/*; do
    [[ -e "$entry" ]] || continue
    name="$(basename "$entry")"
    rm -rf -- "${release_dir:?}/${name:?}"
    ln -sfn "../../shared/$name" "$release_dir/$name"
  done
  shopt -u dotglob
}

cmd_deploy() {
  local app="$1" release_id="$2"
  validate_app "$app"
  validate_release_id "$release_id"

  local releases_dir="$APPS_ROOT/$app/releases"
  local target_dir="$releases_dir/$release_id"

  mkdir -p "$releases_dir"
  [[ -e "$target_dir" ]] && die "release already exists: $release_id"
  mkdir -p "$target_dir"

  # Tarball of the built app (mkdocs site/, a React dist/, or a Laravel
  # checkout with vendor/ built) arrives on stdin.
  tar -xzf - -C "$target_dir"

  link_shared "$app" "$target_dir"

  # Atomic symlink swap: build the new link next to the old one, then rename over it.
  local tmp_link="$APPS_ROOT/$app/current.tmp.$$"
  ln -sfn "$target_dir" "$tmp_link"
  mv -T "$tmp_link" "$APPS_ROOT/$app/current"

  prune_releases "$app"

  echo "deployed $app -> $release_id"
}

cmd_rollback() {
  local app="$1" release_id="$2"
  validate_app "$app"
  validate_release_id "$release_id"

  local target_dir="$APPS_ROOT/$app/releases/$release_id"
  [[ -d "$target_dir" ]] || die "release not found (was it pruned?): $release_id"

  local tmp_link="$APPS_ROOT/$app/current.tmp.$$"
  ln -sfn "$target_dir" "$tmp_link"
  mv -T "$tmp_link" "$APPS_ROOT/$app/current"

  echo "rolled back $app -> $release_id"
}

cmd_list() {
  local app="$1"
  validate_app "$app"
  local current_target
  current_target="$(readlink -f "$APPS_ROOT/$app/current" 2>/dev/null || true)"
  for rel in $(ls -1t "$APPS_ROOT/$app/releases"); do
    if [[ "$APPS_ROOT/$app/releases/$rel" == "$current_target" ]]; then
      echo "* $rel (current)"
    else
      echo "  $rel"
    fi
  done
}

# SSH_ORIGINAL_COMMAND is set by sshd from whatever command the client
# requested; the "command=" in authorized_keys forced us to run this
# script instead, so this is the only place client input enters.
read -r -a args <<< "${SSH_ORIGINAL_COMMAND:-}"
sub="${args[0]:-}"

case "$sub" in
  deploy)   cmd_deploy "${args[1]:-}" "${args[2]:-}" ;;
  rollback) cmd_rollback "${args[1]:-}" "${args[2]:-}" ;;
  list)     cmd_list "${args[1]:-}" ;;
  *)        die "usage: deploy <app> <release_id> | rollback <app> <release_id> | list <app>" ;;
esac
