#!/usr/bin/env bash
set -euo pipefail

XUI_DIR="/usr/local/x-ui"
XUI_BIN="${XUI_DIR}/x-ui"
BACKUP_DIR="${XUI_DIR}/.client-host-backups"
LOCK_FILE="${XUI_DIR}/.client-host-update.lock"
MANAGED_UPDATER="/usr/local/sbin/update-client-host"
REPO="bigbossstard/3x-ui"
API_URL="https://api.github.com/repos/${REPO}"
CURRENT_REF_URL="${API_URL}/git/ref/tags/client-host-current"
HEAD_REF_URL="${API_URL}/git/ref/heads/client-host"

die() {
  echo "ERROR: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "Required command not found: $1"
}

api_get() {
  curl -fsSL     --retry 6     --retry-all-errors     --retry-delay 2     --connect-timeout 15     --speed-limit 1     --speed-time 60     -H "Accept: application/vnd.github+json"     -H "User-Agent: 3x-ui-client-host-updater"     "$1"
}

ref_sha() {
  local url="$1" body sha
  body="$(api_get "${url}?cachebust=$(date +%s%N)")" || return 1
  sha="$(printf '%s\n' "$body" | grep -oE '"sha"[[:space:]]*:[[:space:]]*"[0-9a-fA-F]{40}"' | head -n1 | sed -E 's/.*"([0-9a-fA-F]{40})".*/\1/' || true)"
  [[ "$sha" =~ ^[0-9a-fA-F]{40}$ ]] || return 1
  printf '%s\n' "${sha,,}"
}

latest_commit() {
  local pointer head attempt
  for attempt in {1..12}; do
    pointer="$(ref_sha "$CURRENT_REF_URL" || true)"
    head="$(ref_sha "$HEAD_REF_URL" || true)"
    if [[ -n "$pointer" && -n "$head" && "$pointer" == "$head" ]]; then
      printf '%s\n' "$head"
      return 0
    fi
    echo "Release pointer is not synchronized with client-host yet; retrying ($attempt/12)..."
    sleep 10
  done
  die "GitHub release pointer is not synchronized with the client-host branch. Refusing to install a stale build."
}

release_url() {
  local commit="$1"
  printf 'https://github.com/%s/releases/download/client-host-%s' "$REPO" "$commit"
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

refresh_managed_updater() {
  local target_commit="$1" base tmp expected actual self_hash
  local self_path
  self_path="$(readlink -f "$0" 2>/dev/null || printf '%s' "$0")"
  [[ "$self_path" == "$MANAGED_UPDATER" ]] || return 0

  base="$(release_url "$target_commit")"
  tmp="$(mktemp "${XUI_DIR}/update-client-host.XXXXXX")"
  expected="$(mktemp)"
  trap 'rm -f "${tmp:-}" "${expected:-}"' RETURN

  curl -fsSL     --retry 6     --retry-all-errors     --retry-delay 2     --connect-timeout 15     --speed-limit 1     --speed-time 60     -o "$tmp" "${base}/update-client-host.sh"
  curl -fsSL     --retry 6     --retry-all-errors     --retry-delay 2     --connect-timeout 15     --speed-limit 1     --speed-time 60     -o "$expected" "${base}/update-client-host.sh.sha256"

  actual="$(sha256sum "$tmp" | awk '{print $1}')"
  expected_hash="$(awk '{print $1}' "$expected" | head -n1)"
  [[ -n "$expected_hash" && "$actual" == "$expected_hash" ]] || die "Updater SHA256 verification failed."

  self_hash="$(sha256sum "$MANAGED_UPDATER" | awk '{print $1}')"
  if [[ "$self_hash" != "$actual" ]]; then
    echo "Updating managed updater..."
    install -m 755 "$tmp" "$MANAGED_UPDATER"
    rm -f "$tmp" "$expected"
    exec "$MANAGED_UPDATER" update
  fi

  rm -f "$tmp" "$expected"
  trap - RETURN
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

  exec 9>"$LOCK_FILE"
  flock -n 9 || die "Another client-host update is already running."

  mkdir -p "$BACKUP_DIR"

  local current_version target_commit target_short base tmp checksum expected actual downloaded_version timestamp backup
  current_version="$("$XUI_BIN" -v 2>/dev/null || echo unknown)"
  target_commit="$(latest_commit)"
  target_short="${target_commit:0:8}"

  if [[ "$current_version" == "dev+${target_short}" ]]; then
    echo "Already up to date: $current_version"
    return 0
  fi

  refresh_managed_updater "$target_commit"

  base="$(release_url "$target_commit")"
  tmp="$(mktemp "${XUI_DIR}/x-ui.client-host.XXXXXX")"
  checksum="$(mktemp)"
  trap 'rm -f "${tmp:-}" "${checksum:-}"' RETURN

  echo "Installed version: $current_version"
  echo "Target commit: $target_commit"
  echo "Downloading immutable client-host release..."

  curl -fsSL     --retry 6     --retry-all-errors     --retry-delay 2     --connect-timeout 15     --speed-limit 1     --speed-time 60     -o "$tmp" "${base}/x-ui"
  curl -fsSL     --retry 6     --retry-all-errors     --retry-delay 2     --connect-timeout 15     --speed-limit 1     --speed-time 60     -o "$checksum" "${base}/x-ui-linux-amd64.sha256"

  expected="$(awk '{print $1}' "$checksum" | head -n1)"
  actual="$(sha256sum "$tmp" | awk '{print $1}')"
  [[ -n "$expected" && "$expected" == "$actual" ]] || die "SHA256 verification failed."

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
  rm -f "$tmp" "$checksum"
  trap - RETURN
}

require_cmd true

case "${1:-update}" in
  update) update ;;
  rollback)
    [[ "$EUID" -eq 0 ]] || die "Run as root."
    require_cmd flock
    exec 9>"$LOCK_FILE"
    flock -n 9 || die "Another client-host update is already running."
    rollback
    ;;
  *)
    echo "Usage: $0 [update|rollback]"
    exit 2
    ;;
esac
