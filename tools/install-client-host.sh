#!/usr/bin/env bash
set -euo pipefail

REPO="bigbossstard/3x-ui"
API_URL="https://api.github.com/repos/${REPO}"
CURRENT_REF_URL="${API_URL}/git/ref/tags/client-host-current"
HEAD_REF_URL="${API_URL}/git/ref/heads/client-host"
MANAGED_UPDATER="/usr/local/sbin/update-client-host"
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
  curl -fsSL     --retry 6     --retry-all-errors     --retry-delay 2     --connect-timeout 15     --speed-limit 1     --speed-time 60     -H "Accept: application/vnd.github+json"     -H "User-Agent: 3x-ui-client-host-installer"     "$1"
}

ref_sha() {
  local url="$1" body sha
  body="$(api_get "${url}?cachebust=$(date +%s%N)")" || return 1
  sha="$(printf '%s\n' "$body" | grep -oE '"sha"[[:space:]]*:[[:space:]]*"[0-9a-fA-F]{40}"' | head -n1 | sed -E 's/.*"([0-9a-fA-F]{40})".*/\1/' || true)"
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

[[ -x /usr/local/x-ui/x-ui ]] || die "/usr/local/x-ui/x-ui not found. This installer is for an existing 3x-ui node."
systemctl cat x-ui >/dev/null 2>&1 || die "systemd service x-ui not found."

tmp_dir="$(mktemp -d /tmp/client-host-installer.XXXXXX)"
trap 'rm -rf "${tmp_dir:-}"' EXIT

echo "Resolving verified client-host release..."
for attempt in {1..12}; do
  pointer="$(ref_sha "$CURRENT_REF_URL" || true)"
  head="$(ref_sha "$HEAD_REF_URL" || true)"
  if [[ -n "$pointer" && -n "$head" && "$pointer" == "$head" ]]; then
    target_commit="$pointer"
    break
  fi
  echo "Release pointer is not synchronized with client-host yet; retrying ($attempt/12)..."
  sleep 10
done

[[ -n "${target_commit:-}" ]] || die "Could not resolve a verified client-host release."

release_base="https://github.com/${REPO}/releases/download/client-host-${target_commit}"
updater="${tmp_dir}/update-client-host.sh"
updater_sum="${tmp_dir}/update-client-host.sh.sha256"

echo "Downloading updater from immutable release ${target_commit}..."
curl -fsSL   --retry 6   --retry-all-errors   --retry-delay 2   --connect-timeout 15   --speed-limit 1   --speed-time 60   -o "$updater"   "${release_base}/update-client-host.sh"

curl -fsSL   --retry 6   --retry-all-errors   --retry-delay 2   --connect-timeout 15   --speed-limit 1   --speed-time 60   -o "$updater_sum"   "${release_base}/update-client-host.sh.sha256"

expected_hash="$(awk '{print $1}' "$updater_sum" | head -n1)"
actual_hash="$(sha256sum "$updater" | awk '{print $1}')"
[[ -n "$expected_hash" && "$expected_hash" == "$actual_hash" ]] || die "Updater SHA256 verification failed."

[[ "$(head -n1 "$updater")" == "#!/usr/bin/env bash" ]] || die "Downloaded updater does not look like the expected script."
install -m 755 "$updater" "$MANAGED_UPDATER"

cat >"$SERVICE_UNIT" <<'EOF'
[Unit]
Description=Update 3x-ui client-host patched binary
After=network-online.target x-ui.service
Wants=network-online.target

[Service]
Type=oneshot
User=root
ExecStart=/usr/local/sbin/update-client-host update
TimeoutStartSec=30min
EOF

cat >"$TIMER_UNIT" <<'EOF'
[Unit]
Description=Daily 3x-ui client-host update check

[Timer]
OnCalendar=*-*-* 04:17:00
RandomizedDelaySec=30min
Persistent=true
Unit=x-ui-client-host-update.service

[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
systemctl enable --now x-ui-client-host-update.timer

echo "Updater installed: $MANAGED_UPDATER"
echo "Automatic update timer enabled."
echo
exec "$MANAGED_UPDATER" update
