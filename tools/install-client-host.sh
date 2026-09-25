#!/usr/bin/env bash
set -euo pipefail

REPO="bigbossstard/3x-ui"
API_URL="https://api.github.com/repos/${REPO}"
CURRENT_REF_URL="${API_URL}/git/ref/tags/client-host-current"
MANAGED_UPDATER="/usr/local/sbin/update-client-host"
MANAGER="/usr/local/bin/xch"
ORIGINAL_BACKUP="/usr/local/x-ui/.client-host-original-x-ui"
ORIGINAL_BACKUP_SHA256="${ORIGINAL_BACKUP}.sha256"
SERVICE_UNIT="/etc/systemd/system/x-ui-client-host-update.service"
TIMER_UNIT="/etc/systemd/system/x-ui-client-host-update.timer"

die() {
  echo "ERROR: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "Required command not found: $1"
}

api_get() {
  curl -4 -fsSL --retry 3 --retry-all-errors --retry-delay 1 --retry-max-time 30     --connect-timeout 10 --speed-limit 1 --speed-time 30 --max-time 180     -H "Accept: application/vnd.github+json"     -H "User-Agent: 3x-ui-client-host-installer" "$1"
}

ref_sha() {
  local url="$1" body sha
  body="$(api_get "${url}?cachebust=$(date +%s%N)")" || return 1
  sha="$(printf '%s\n' "$body" |
    grep -oE '"sha"[[:space:]]*:[[:space:]]*"[0-9a-fA-F]{40}"' |
    head -n1 |
    sed -E 's/.*"([0-9a-fA-F]{40})".*/\1/' || true)"
  [[ "$sha" =~ ^[0-9a-fA-F]{40}$ ]] || return 1
  printf '%s\n' "${sha,,}"
}

[[ "$EUID" -eq 0 ]] || die "Run as root."
[[ "$(uname -m)" == "x86_64" ]] || die "This installer currently supports only x86_64."

require_cmd curl
require_cmd grep
require_cmd head
require_cmd sed
require_cmd awk
require_cmd sha256sum
require_cmd install
require_cmd systemctl

[[ -x /usr/local/x-ui/x-ui ]] ||
  die "/usr/local/x-ui/x-ui not found. This installer is for an existing 3x-ui node."
systemctl cat x-ui >/dev/null 2>&1 || die "systemd service x-ui not found."

current_version="$("/usr/local/x-ui/x-ui" -v 2>/dev/null || echo unknown)"
if [[ -e "$ORIGINAL_BACKUP" || -e "$ORIGINAL_BACKUP_SHA256" ]]; then
  [[ -s "$ORIGINAL_BACKUP" && -s "$ORIGINAL_BACKUP_SHA256" ]] ||
    die "Existing original 3x-ui backup is incomplete."
  expected_original="$(awk '{print $1}' "$ORIGINAL_BACKUP_SHA256" | head -n1)"
  actual_original="$(sha256sum "$ORIGINAL_BACKUP" | awk '{print $1}')"
  [[ -n "$expected_original" && "$actual_original" == "$expected_original" ]] ||
    die "Existing original 3x-ui backup SHA256 verification failed."
else
  [[ "$current_version" != dev+* ]] ||
    die "A client-host style dev+ binary is already installed, but no original 3x-ui backup exists. Restore the original 3x-ui binary before installing Client-Host."
  install -m 755 /usr/local/x-ui/x-ui "$ORIGINAL_BACKUP"
  sha256sum "$ORIGINAL_BACKUP" > "$ORIGINAL_BACKUP_SHA256"
fi

tmp_dir="$(mktemp -d /tmp/client-host-installer.XXXXXX)"
trap 'rm -rf "${tmp_dir:-}"' EXIT

echo "Resolving current client-host release..."
target_commit="$(ref_sha "$CURRENT_REF_URL" || true)"
[[ -n "$target_commit" ]] || die "Could not resolve the current client-host release."

release_base="https://github.com/${REPO}/releases/download/client-host-${target_commit}"

download_verified() {
  local name="$1"
  local target="${tmp_dir}/${name}"
  local checksum="${target}.sha256"
  local expected actual

  curl -4 -fsSL --retry 3 --retry-all-errors --retry-delay 1 --retry-max-time 30     --connect-timeout 10 --speed-limit 1 --speed-time 30 --max-time 180     -o "$target" "${release_base}/${name}"
  curl -4 -fsSL --retry 3 --retry-all-errors --retry-delay 1 --retry-max-time 30     --connect-timeout 10 --speed-limit 1 --speed-time 30 --max-time 180     -o "$checksum" "${release_base}/${name}.sha256"

  expected="$(awk '{print $1}' "$checksum" | head -n1)"
  actual="$(sha256sum "$target" | awk '{print $1}')"
  [[ -n "$expected" && "$expected" == "$actual" ]] ||
    die "SHA256 verification failed for $name."

  printf '%s\n' "$target"
}

echo "Downloading verified client-host manager and updater..."
updater="$(download_verified update-client-host.sh)"
manager="$(download_verified xch.sh)"

[[ "$(head -n1 "$updater")" == "#!/usr/bin/env bash" ]] ||
  die "Downloaded updater does not look like the expected script."
[[ "$(head -n1 "$manager")" == "#!/usr/bin/env bash" ]] ||
  die "Downloaded manager does not look like the expected script."

install -m 755 "$updater" "$MANAGED_UPDATER"
install -m 755 "$manager" "$MANAGER"

# Disable/remove the old automatic timer from previous installer versions.
if systemctl list-unit-files --type=timer --all 2>/dev/null |
  grep -q '^x-ui-client-host-update.timer'; then
  systemctl disable --now x-ui-client-host-update.timer >/dev/null 2>&1 || true
fi
rm -f "$SERVICE_UNIT" "$TIMER_UNIT"
systemctl daemon-reload

echo
echo "client-host manager installed: $MANAGER"
echo "Automatic updates: disabled."
echo "Run 'xch' to open the menu."
echo
exec "$MANAGER"
