#!/usr/bin/env bash
set -euo pipefail

REPO="bigbossstard/3x-ui"
RAW_UPDATER_URL="https://raw.githubusercontent.com/${REPO}/client-host/tools/update-client-host.sh"
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

[[ "$EUID" -eq 0 ]] || die "Run as root."
[[ "$(uname -m)" == "x86_64" ]] || die "This installer currently supports only x86_64."

require_cmd curl
require_cmd install
require_cmd systemctl

tmp="$(mktemp)"
trap 'rm -f "${tmp:-}"' EXIT

echo "Installing client-host updater..."
curl -fsSL   --retry 6   --retry-all-errors   --retry-delay 2   --connect-timeout 15   --speed-limit 1   --speed-time 60   -o "$tmp" "${RAW_UPDATER_URL}?cachebust=$(date +%s%N)"

[[ "$(head -n1 "$tmp")" == "#!/usr/bin/env bash" ]] || die "Downloaded updater does not look like the expected script."
install -m 755 "$tmp" "$MANAGED_UPDATER"

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
