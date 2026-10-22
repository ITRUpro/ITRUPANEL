# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
# Логотип и проверка сервера перед установкой. Подключается из install.sh; тот же текст вставляется в онлайн-установщик
# (release/bootstrap.sh.in, метка ITRUPANEL_BANNER_PLACEHOLDER). Только выводит сведения, ничего не меняет.
# ITRUPANEL-BANNER-BEGIN
itru_lang() { case "${LC_ALL:-${LC_MESSAGES:-${LANG:-}}}" in ru*) echo ru ;; *) echo en ;; esac; }
ITRU_LANG="$(itru_lang)"
itru_t() { if [ "$ITRU_LANG" = ru ]; then printf '%s' "$1"; else printf '%s' "$2"; fi; }
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  IC_B=$'\e[1;36m'; IC_OK=$'\e[0;32m'; IC_WARN=$'\e[1;33m'; IC_ERR=$'\e[1;31m'; IC_0=$'\e[0m'
else
  IC_B=''; IC_OK=''; IC_WARN=''; IC_ERR=''; IC_0=''
fi

itru_logo() {
  printf '%s' "$IC_B"
  cat <<'ITRU_LOGO'
    ______________  __  ______  ___    _   __________ 
   /  _/_  __/ __ \/ / / / __ \/   |  / | / / ____/ / 
   / /  / / / /_/ / / / / /_/ / /| | /  |/ / __/ / /  
 _/ /  / / / _, _/ /_/ / ____/ ___ |/ /|  / /___/ /___
/___/ /_/ /_/ |_|\____/_/   /_/  |_/_/ |_/_____/_____/
ITRU_LOGO
  printf '%s\n' "$IC_0"
}

# itru_row «метка» «значение» [ok|warn|err]
itru_row() {
  local c="$IC_OK"
  case "${3:-ok}" in warn) c="$IC_WARN" ;; err) c="$IC_ERR" ;; esac
  # Выравнивание по символам (не зависит от установленных локалей): русские подписи выводятся только в UTF-8-локали (LANG=ru_*), иначе подписи английские.
  local n pad
  n=$(printf '%s' "$1" | LC_ALL=C tr -d '\200-\277' | wc -c | tr -d ' ')
  pad=$((22 - n)); [ "$pad" -gt 1 ] || pad=1
  printf '%s%s%*s%s%s%s%s\n' "$IC_B" "$1" "$pad" '' "$IC_0" "$c" "$2" "$IC_0"
}

# Приветствие с логотипом: один раз за запуск (онлайн-установщик показывает его первым и передаёт ITRUPANEL_BANNER=1).
itru_banner() {
  [ -z "${ITRUPANEL_BANNER:-}" ] || return 0
  export ITRUPANEL_BANNER=1
  echo
  itru_logo
  printf '%s%s%s\n\n' "$IC_B" "$(itru_t 'Добро пожаловать! Сейчас я установлю для вас панель управления сервером.' 'Greetings! Now I will install the server control panel for you.')" "$IC_0"
}

# Проверка сервера перед установкой: ОС, архитектура, права, systemd, память, диск, доступ в интернет, порты.
# Предупреждения не останавливают установку; жёсткие требования проверяет сам установщик.
# Аргументы: название ОС, архитектура, режим (install|update).
itru_preflight() {
  local os="$1" arch="$2" mode="${3:-install}" mem_mb swap_mb disk_gb busy="" p
  printf '%s%s%s\n' "$IC_B" "$(itru_t 'Проверка сервера перед установкой' 'Start pre-installation checks')" "$IC_0"
  itru_row "OS:" "$os" ok
  itru_row "$(itru_t 'Архитектура:' 'Architecture:')" "$arch" ok
  if [ "$(id -u)" = 0 ]; then itru_row "root:" "$(itru_t 'да' 'yes')" ok; else itru_row "root:" "$(itru_t 'нет, нужен root' 'no, root is required')" err; fi
  if [ -d /run/systemd/system ]; then itru_row "systemd:" "$(itru_t 'да' 'yes')" ok; else itru_row "systemd:" "$(itru_t 'нет, нужен systemd' 'no, systemd is required')" err; fi
  mem_mb=$(awk '/^MemTotal:/{print int($2/1024)}' /proc/meminfo 2>/dev/null || echo 0)
  swap_mb=$(awk '/^SwapTotal:/{print int($2/1024)}' /proc/meminfo 2>/dev/null || echo 0)
  if [ "${mem_mb:-0}" -ge 1000 ]; then itru_row "$(itru_t 'Память:' 'Memory:')" "${mem_mb} MB (swap ${swap_mb:-0} MB)" ok
  else itru_row "$(itru_t 'Память:' 'Memory:')" "${mem_mb:-0} MB (swap ${swap_mb:-0} MB) — $(itru_t 'мало, рекомендуется от 1 ГБ' 'low, 1 GB or more is recommended')" warn; fi
  disk_gb=$(df -Pk /var 2>/dev/null | awk 'NR==2{print int($4/1048576)}' || echo 0)
  if [ "${disk_gb:-0}" -ge 5 ]; then itru_row "$(itru_t 'Свободно на диске:' 'Free disk space:')" "${disk_gb} GB" ok
  else itru_row "$(itru_t 'Свободно на диске:' 'Free disk space:')" "${disk_gb:-0} GB — $(itru_t 'мало, рекомендуется от 5 ГБ' 'low, 5 GB or more is recommended')" warn; fi
  if command -v curl >/dev/null 2>&1; then
    if curl -sS -o /dev/null -I --max-time 6 https://itrupanel.com/ >/dev/null 2>&1; then itru_row "$(itru_t 'Интернет:' 'Internet:')" "$(itru_t 'есть доступ к itrupanel.com' 'itrupanel.com is reachable')" ok
    else itru_row "$(itru_t 'Интернет:' 'Internet:')" "$(itru_t 'itrupanel.com не отвечает: проверьте сеть и DNS' 'itrupanel.com does not answer: check the network and DNS')" warn; fi
  fi
  if [ "$mode" = install ] && command -v ss >/dev/null 2>&1; then
    for p in 80 443 3333; do ss -ltn 2>/dev/null | awk -v p=":$p" '$4 ~ p"$" {found=1} END{exit !found}' && busy="$busy $p"; done
    if [ -z "$busy" ]; then itru_row "$(itru_t 'Порты 80, 443, 3333:' 'Ports 80, 443, 3333:')" "$(itru_t 'свободны' 'free')" ok
    else itru_row "$(itru_t 'Порты 80, 443, 3333:' 'Ports 80, 443, 3333:')" "$(itru_t 'заняты:' 'in use:')${busy} — $(itru_t 'установка рассчитана на чистый сервер' 'the installation is meant for a clean server')" warn; fi
  fi
  echo
}
# ITRUPANEL-BANNER-END
