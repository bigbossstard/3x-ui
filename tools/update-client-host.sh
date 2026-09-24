#!/usr/bin/env bash
set -euo pipefail

XUI_DIR="/usr/local/x-ui"
XUI_BIN="${XUI_DIR}/x-ui"
BACKUP_DIR="${XUI_DIR}/.client-host-backups"
ORIGINAL_BACKUP="${XUI_DIR}/.client-host-original-x-ui"
ORIGINAL_BACKUP_SHA256="${ORIGINAL_BACKUP}.sha256"
LOCK_FILE="${XUI_DIR}/.client-host-update.lock"
MANAGED_UPDATER="/usr/local/sbin/update-client-host"
MANAGER="/usr/local/bin/xch"
REPO="bigbossstard/3x-ui"
API_URL="https://api.github.com/repos/${REPO}"
CURRENT_REF_URL="${API_URL}/git/ref/tags/client-host-current"

die() {
  echo "ERROR: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "Required command not found: $1"
}

api_get() {
  curl -4 -fsSL     --retry 3     --retry-all-errors     --retry-delay 1     --retry-max-time 30     --connect-timeout 10     --speed-limit 1     --speed-time 30     --max-time 45     -H "Accept: application/vnd.github+json"     -H "User-Agent: 3x-ui-client-host-updater"     "$1"
}

ref_sha() {
  local url="$1" body sha
  body="$(api_get "${url}?cachebust=$(date +%s%N)")" || return 1
  sha="$(printf '%s\n' "$body" | grep -oE '"sha"[[:space:]]*:[[:space:]]*"[0-9a-fA-F]{40}"' | head -n1 | sed -E 's/.*"([0-9a-fA-F]{40})".*/\1/' || true)"
  [[ "$sha" =~ ^[0-9a-fA-F]{40}$ ]] || return 1
  printf '%s\n' "${sha,,}"
}

latest_commit() {
  local pointer
  pointer="$(ref_sha "$CURRENT_REF_URL" || true)"
  [[ -n "$pointer" ]] || die "Could not resolve the current client-host release pointer."
  printf '%s\n' "$pointer"
}

release_url() {
  local commit="$1"
  printf 'https://github.com/%s/releases/download/client-host-%s' "$REPO" "$commit"
}

download_verified_asset() {
  local base="$1" name="$2" target="$3" checksum_name="${4:-${name}.sha256}"
  local checksum="${target}.sha256" expected actual

  curl -4 -fsSL --retry 3 --retry-all-errors --retry-delay 1 --retry-max-time 30 \
    --connect-timeout 10 --speed-limit 1 --speed-time 30 --max-time 60 \
    -o "$target" "${base}/${name}"
  curl -4 -fsSL --retry 3 --retry-all-errors --retry-delay 1 --retry-max-time 30 \
    --connect-timeout 10 --speed-limit 1 --speed-time 30 --max-time 60 \
    -o "$checksum" "${base}/${checksum_name}"

  expected="$(awk '{print $1}' "$checksum" | head -n1)"
  [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] ||
    die "Invalid SHA256 file for $name."
  actual="$(sha256sum "$target" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]] ||
    die "SHA256 verification failed for $name."
}

wait_for_service() {
  local attempt
  for attempt in {1..15}; do
    if systemctl is-active --quiet x-ui; then
      return 0
    fi
    sleep 1
  done
  return 1
}

uninstall() {
  [[ "$EUID" -eq 0 ]] || die "Run as root."
  [[ -x "$XUI_BIN" ]] || die "$XUI_BIN not found."
  systemctl cat x-ui >/dev/null 2>&1 || die "systemd service x-ui not found."
  require_cmd sha256sum
  require_cmd install
  require_cmd systemctl
  require_cmd flock
  require_cmd awk
  require_cmd head

  local expected actual current_backup current_version timestamp original_backup
  if [[ -s "$ORIGINAL_BACKUP" && -s "$ORIGINAL_BACKUP_SHA256" ]]; then
    expected="$(awk '{print $1}' "$ORIGINAL_BACKUP_SHA256" | head -n1)"
    actual="$(sha256sum "$ORIGINAL_BACKUP" | awk '{print $1}')"
    [[ -n "$expected" && "$actual" == "$expected" ]] ||
      die "Original 3x-ui backup SHA256 verification failed."
    original_backup="$ORIGINAL_BACKUP"
  else
    original_backup="$(find "$BACKUP_DIR" -maxdepth 1 -type f -name 'x-ui.*' -printf '%p
' 2>/dev/null |
      sort | while IFS= read -r candidate; do
        version="$( "$candidate" -v 2>/dev/null || true )"
        [[ -n "$version" && "$version" != dev+* ]] && { printf '%s
' "$candidate"; break; }
      done)"
    [[ -s "$original_backup" ]] ||
      die "Original 3x-ui binary backup not found. Refusing to uninstall without a saved pre-client-host binary."
  fi
  

  exec 9>"$LOCK_FILE"
  flock -n 9 || die "Another client-host operation is already running."

  mkdir -p "$BACKUP_DIR"
  current_version="$("$XUI_BIN" -v 2>/dev/null || echo unknown)"
  timestamp="$(date -u +%Y%m%d-%H%M%S-%N)"
  current_backup="${BACKUP_DIR}/x-ui.uninstall-${timestamp}"
  cp -a "$XUI_BIN" "$current_backup"

  echo "Restoring original 3x-ui binary..."
  echo "Current patched version: $current_version"
  echo "Original backup: $original_backup"

  systemctl stop x-ui || die "Failed to stop x-ui."
  install -m 755 "$original_backup" "$XUI_BIN"
  systemctl start x-ui || true

  if ! wait_for_service; then
    echo "Original binary failed to become active. Restoring previous binary..."
    systemctl stop x-ui || true
    install -m 755 "$current_backup" "$XUI_BIN"
    systemctl start x-ui || true
    wait_for_service || true
    die "Uninstall rolled back automatically; client-host remains installed."
  fi

  echo "Original 3x-ui binary is running."
  echo "Version reported by binary: $("$XUI_BIN" -v 2>/dev/null || true)"
  echo "Database/config were not replaced."
}

prune_backups() {
  local -a old_backups=()
  mapfile -t old_backups < <(
    find "$BACKUP_DIR" -maxdepth 1 -type f -name 'x-ui.*' -printf '%f\n' 2>/dev/null |
      sort -r |
      tail -n +6
  )
  if (( ${#old_backups[@]} )); then
    rm -f -- "${old_backups[@]}"
  fi
}

rollback() {
  mkdir -p "$BACKUP_DIR"
  local backup_name backup
  backup_name="$(find "$BACKUP_DIR" -maxdepth 1 -type f -name 'x-ui.*' -printf '%f\n' 2>/dev/null | sort -r | head -n1 || true)"
  backup="${BACKUP_DIR}/${backup_name}"
  [[ -n "$backup_name" && -s "$backup" ]] || die "No client-host backup found in $BACKUP_DIR"

  echo "Restoring: $backup"
  systemctl stop x-ui || true
  install -m 755 "$backup" "$XUI_BIN"
  systemctl start x-ui || true
  wait_for_service || {
    systemctl status x-ui --no-pager || true
    die "Rollback binary was restored, but x-ui did not start"
  }
  echo "Rollback completed."
}

refresh_managed_tools() {
  local target_commit="$1" self_path updater_tmp manager_tmp updater_changed=0
  self_path="$(readlink -f "$0" 2>/dev/null || printf "%s" "$0")"
  [[ "$self_path" == "$MANAGED_UPDATER" ]] || return 0

  updater_tmp="${TMP_DIR}/update-client-host.sh"
  manager_tmp="${TMP_DIR}/xch.sh"
  local base
  base="$(release_url "$target_commit")"

  echo "Checking managed client-host tools..."
  download_verified_asset "$base" "update-client-host.sh" "$updater_tmp"
  bash -n "$updater_tmp" || die "Downloaded updater failed shell syntax validation."

  if ! cmp -s "$updater_tmp" "$MANAGED_UPDATER"; then
    echo "Installing newer managed updater..."
    install -m 755 "$updater_tmp" "$MANAGED_UPDATER"
    updater_changed=1
  fi

  if [[ -x "$MANAGER" ]]; then
    download_verified_asset "$base" "xch.sh" "$manager_tmp"
    bash -n "$manager_tmp" || die "Downloaded manager failed shell syntax validation."
    if ! cmp -s "$manager_tmp" "$MANAGER"; then
      echo "Installing newer xch manager..."
      install -m 755 "$manager_tmp" "$MANAGER"
    fi
  fi

  if (( updater_changed )); then
    rm -rf "$TMP_DIR"
    exec "$MANAGED_UPDATER" update
  fi
}

update() {
  [[ "$EUID" -eq 0 ]] || die "Run as root."
  [[ "$(uname -m)" == "x86_64" ]] || die "This updater currently supports only x86_64."
  [[ -x "$XUI_BIN" ]] || die "$XUI_BIN not found."
  systemctl cat x-ui >/dev/null 2>&1 || die "systemd service x-ui not found."
  require_cmd curl
  require_cmd sha256sum
  require_cmd install
  require_cmd systemctl
  require_cmd flock
  require_cmd readlink

  exec 9>"$LOCK_FILE"
  flock -n 9 || die "Another client-host update is already running."

  mkdir -p "$BACKUP_DIR"
  local current_version target_commit target_short base tmp downloaded_version timestamp backup
  current_version="$( "$XUI_BIN" -v 2>/dev/null || echo unknown )"
  target_commit="$(latest_commit)"
  target_short="${target_commit:0:8}"

  TMP_DIR="$(mktemp -d "${XUI_DIR}/.client-host-update.XXXXXX")"
  trap 'rm -rf "${TMP_DIR:-}"' EXIT

  if [[ "$current_version" == "dev+${target_short}" ]]; then
    echo "Already up to date: $current_version"
    return 0
  fi

  refresh_managed_tools "$target_commit"

  base="$(release_url "$target_commit")"
  tmp="${TMP_DIR}/x-ui"

  echo "Installed version: $current_version"
  echo "Target commit: $target_commit"
  echo "Downloading immutable client-host release..."

  download_verified_asset "$base" "x-ui" "$tmp" "x-ui-linux-amd64.sha256"

  chmod 755 "$tmp"
  downloaded_version="$( "$tmp" -v 2>/dev/null || true )"
  [[ "$downloaded_version" == "dev+${target_short}" ]] || die "Downloaded binary reports '$downloaded_version', expected 'dev+${target_short}'."

  timestamp="$(date -u +%Y%m%d-%H%M%S-%N)"
  backup="${BACKUP_DIR}/x-ui.${timestamp}"
  cp -a "$XUI_BIN" "$backup"
  echo "Backup: $backup"

  systemctl stop x-ui || die "Failed to stop x-ui."
  install -m 755 "$tmp" "$XUI_BIN"
  systemctl start x-ui || true

  if ! wait_for_service; then
    echo "New binary failed to become active. Restoring backup..."
    systemctl stop x-ui || true
    install -m 755 "$backup" "$XUI_BIN"
    systemctl start x-ui || true
    systemctl status x-ui --no-pager || true
    die "Update rolled back automatically."
  fi

  echo "Patched x-ui is running."
  echo "Version reported by binary: $("$XUI_BIN" -v 2>/dev/null || true)"
  echo "Target commit: $target_commit"
  echo "Database/config were not replaced."
  prune_backups
}

case "${1:-update}" in
  update) update ;;
  uninstall)
    uninstall
    ;;
  rollback)
    [[ "$EUID" -eq 0 ]] || die "Run as root."
    require_cmd flock
    require_cmd find
    require_cmd install
    require_cmd systemctl
    exec 9>"$LOCK_FILE"
    flock -n 9 || die "Another client-host update is already running."
    rollback
    ;;
  *)
    echo "Usage: $0 [update|rollback|uninstall]"
    exit 2
    ;;
esac
