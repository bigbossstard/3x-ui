#!/usr/bin/env bash
set -euo pipefail

XUI_DIR="/usr/local/x-ui"
XUI_BIN="${XUI_DIR}/x-ui"
BACKUP_DIR="${XUI_DIR}/.client-host-backups"
RELEASE_URL="https://github.com/bigbossstard/3x-ui/releases/download/client-host"
ASSET_URL="${RELEASE_URL}/x-ui"
CHECKSUM_URL="${RELEASE_URL}/x-ui-linux-amd64.sha256"

die() {
  echo "ERROR: $*" >&2
  exit 1
}

rollback() {
  mkdir -p "$BACKUP_DIR"
  local backup
  backup="$(find "$BACKUP_DIR" -maxdepth 1 -type f -name 'x-ui.*' -printf '%T@ %p\n' 2>/dev/null | sort -nr | head -n1 | cut -d' ' -f2- || true)"
  [[ -n "$backup" && -s "$backup" ]] || die "No client-host backup found in $BACKUP_DIR"

  echo "Restoring: $backup"
  systemctl stop x-ui
  install -m 755 "$backup" "$XUI_BIN"
  systemctl start x-ui
  systemctl is-active --quiet x-ui || {
    systemctl status x-ui --no-pager || true
    die "Rollback binary was restored, but x-ui did not start"
  }
  echo "Rollback completed."
}

update() {
  [[ "$EUID" -eq 0 ]] || die "Run as root."
  [[ "$(uname -m)" == "x86_64" ]] || die "This updater currently supports only x86_64."

  [[ -x "$XUI_BIN" ]] || die "$XUI_BIN not found."
  systemctl cat x-ui >/dev/null 2>&1 || die "systemd service x-ui not found."

  mkdir -p "$BACKUP_DIR"
  local current_version timestamp backup tmp checksum expected actual
  current_version="$("$XUI_BIN" -v 2>/dev/null || echo unknown)"
  timestamp="$(date -u +%Y%m%d-%H%M%S)"
  backup="$BACKUP_DIR/x-ui.$timestamp"
  tmp="$(mktemp "$XUI_DIR/x-ui.client-host.XXXXXX")"
  checksum="$(mktemp)"
  trap 'rm -f "$tmp" "$checksum"' EXIT

  echo "Installed version: $current_version"
  echo "Downloading patched x-ui..."
  curl -fL --retry 5 --retry-all-errors --connect-timeout 15 --speed-limit 1 --speed-time 60 -o "$tmp" "$ASSET_URL"
  curl -fL --retry 5 --retry-all-errors --connect-timeout 15 --speed-limit 1 --speed-time 60 -o "$checksum" "$CHECKSUM_URL"

  expected="$(awk '{print $1}' "$checksum" | head -n1)"
  actual="$(sha256sum "$tmp" | awk '{print $1}')"
  [[ -n "$expected" && "$expected" == "$actual" ]] || die "SHA256 verification failed."

  chmod 755 "$tmp"
  "$tmp" -v >/dev/null 2>&1 || die "Downloaded file is not a working x-ui binary."

  cp -a "$XUI_BIN" "$backup"
  echo "Backup: $backup"

  systemctl stop x-ui
  install -m 755 "$tmp" "$XUI_BIN"
  systemctl start x-ui

  if ! systemctl is-active --quiet x-ui; then
    echo "New binary failed to start. Restoring backup..."
    systemctl stop x-ui || true
    install -m 755 "$backup" "$XUI_BIN"
    systemctl start x-ui || true
    systemctl status x-ui --no-pager || true
    die "Update rolled back automatically."
  fi

  echo "Patched x-ui is running."
  echo "Version reported by binary: $("${XUI_BIN}" -v 2>/dev/null || true)"
  echo "Database/config were not replaced."
}

case "${1:-update}" in
  update) update ;;
  rollback) rollback ;;
  *)
    echo "Usage: $0 [update|rollback]"
    exit 2
    ;;
esac
