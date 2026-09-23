#!/usr/bin/env bash
set -euo pipefail

XUI_BIN="/usr/local/x-ui/x-ui"
UPDATER="/usr/local/sbin/update-client-host"
MANAGER="/usr/local/bin/xch"
SERVICE_UNIT="/etc/systemd/system/x-ui-client-host-update.service"
TIMER_UNIT="/etc/systemd/system/x-ui-client-host-update.timer"
BACKUP_DIR="/usr/local/x-ui/.client-host-backups"

REPO="bigbossstard/3x-ui"
API_URL="https://api.github.com/repos/${REPO}"
CURRENT_REF_URL="${API_URL}/git/ref/tags/client-host-current"
HEAD_REF_URL="${API_URL}/git/ref/heads/client-host"

api_get() {
  curl -fsSL --retry 6 --retry-all-errors --retry-delay 2 \
    --connect-timeout 15 --speed-limit 1 --speed-time 60 \
    -H "Accept: application/vnd.github+json" \
    -H "User-Agent: 3x-ui-client-host-manager" "$1"
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

run_root() {
  if [[ "$EUID" -eq 0 ]]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo "$@"
  else
    echo "ERROR: root privileges are required." >&2
    return 1
  fi
}

status_view() {
  local installed pointer head service
  clear
  echo "╔══════════════════════════════════════════════╗"
  echo "║          3x-ui Client-Host Manager          ║"
  echo "╚══════════════════════════════════════════════╝"
  echo

  if [[ -x "$XUI_BIN" ]]; then
    installed="$("$XUI_BIN" -v 2>/dev/null || echo unknown)"
    echo "Установленный x-ui : $installed"
  else
    echo "Установленный x-ui : НЕ НАЙДЕН"
  fi

  service="$(systemctl is-active x-ui 2>/dev/null || true)"
  echo "Сервис x-ui        : ${service:-unknown}"

  if [[ -x "$UPDATER" ]]; then
    pointer="$(ref_sha "$CURRENT_REF_URL" || true)"
    head="$(ref_sha "$HEAD_REF_URL" || true)"
    if [[ -n "$pointer" && -n "$head" && "$pointer" == "$head" ]]; then
      echo "Текущий client-host : ${pointer:0:12}"
      if [[ -n "${installed:-}" && "$installed" == "dev+${pointer:0:8}" ]]; then
        echo "Статус обновления   : АКТУАЛЬНО"
      else
        echo "Статус обновления   : ДОСТУПНО ОБНОВЛЕНИЕ"
      fi
    else
      echo "Статус release      : не синхронизирован"
    fi
  else
    echo "Updater             : не установлен"
  fi

  echo
  read -r -p "Нажмите Enter для возврата в меню..."
}

update_now() {
  clear
  echo "Проверка и установка обновления..."
  echo
  run_root "$UPDATER" update
  echo
  read -r -p "Нажмите Enter для возврата в меню..."
}

rollback_now() {
  clear
  echo "Откат последнего сохранённого бинарника..."
  echo
  run_root "$UPDATER" rollback
  echo
  read -r -p "Нажмите Enter для возврата в меню..."
}

remove_manager() {
  clear
  echo "Удаление управления client-host"
  echo
  echo "Будут удалены:"
  echo "  - команда xch"
  echo "  - managed updater"
  echo "  - старый systemd timer/service (если остались)"
  echo
  echo "Текущий бинарник /usr/local/x-ui/x-ui НЕ удаляется."
  echo "Удаление менеджера не должно ломать 3x-ui."
  echo
  read -r -p "Продолжить? [y/N] " answer
  case "${answer,,}" in
    y|yes)
      if systemctl list-unit-files --type=timer --all 2>/dev/null |
        grep -q '^x-ui-client-host-update.timer'; then
        run_root systemctl disable --now x-ui-client-host-update.timer >/dev/null 2>&1 || true
      fi
      run_root rm -f "$SERVICE_UNIT" "$TIMER_UNIT" "$UPDATER" "$MANAGER"
      run_root systemctl daemon-reload
      echo
      echo "Client-host manager удалён."
      exit 0
      ;;
    *)
      echo
      echo "Отменено."
      read -r -p "Нажмите Enter для возврата в меню..."
      ;;
  esac
}

menu() {
  while true; do
    clear
    echo "╔══════════════════════════════════════════════╗"
    echo "║          3x-ui Client-Host Manager          ║"
    echo "╚══════════════════════════════════════════════╝"
    echo
    echo "  1) Обновить"
    echo "  2) Откатить"
    echo "  3) Статус"
    echo "  4) Удалить"
    echo "  0) Выход"
    echo
    read -r -p "Выберите действие [0-4]: " choice
    echo

    case "$choice" in
      1) update_now ;;
      2) rollback_now ;;
      3) status_view ;;
      4) remove_manager ;;
      0) exit 0 ;;
      *) echo "Неверный выбор."; sleep 1 ;;
    esac
  done
}

case "${1:-menu}" in
  menu) menu ;;
  update) run_root "$UPDATER" update ;;
  rollback) run_root "$UPDATER" rollback ;;
  status) status_view ;;
  remove) remove_manager ;;
  *) echo "Использование: xch [update|rollback|status|remove]" >&2; exit 2 ;;
esac
