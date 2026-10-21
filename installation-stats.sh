#!/usr/bin/env bash
# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
# Best-effort, one-time installation statistics. No credentials or domains.
itru_stats_prepare() (
  set -e
  umask 077
  local dir="$HOME_DIR/etc" marker="$HOME_DIR/etc/installation-stat-id"
  if [ "${ITRUPANEL_INSTALL_STATS:-1}" = 0 ]; then
    mkdir -p "$dir"
    : > "$dir/installation-stat-disabled"
    return
  fi
  [ ! -e "$dir/installation-stat-disabled" ] || return 0
  # An existing installation without our marker predates statistics.
  if [ -e "$HOME_DIR/itrupanel" ] || [ -e "$dir/state.json" ]; then return 0; fi
  mkdir -p "$dir"
  [ -s "$marker" ] || { local id; id=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n'); [ "${#id}" = 64 ]; printf '%s\n' "$id" > "$marker"; }
)
itru_stats_report() (
  set -e
  umask 077
  local dir="$HOME_DIR/etc" id version response ready=0
  [ "${ITRUPANEL_INSTALL_STATS:-1}" != 0 ] || return 0
  [ ! -e "$dir/installation-stat-disabled" ] || return 0
  [ -s "$dir/installation-stat-id" ] && [ ! -e "$dir/installation-stat-sent" ] || return 0
  exec 9>"$dir/installation-stat.lock"
  flock -n 9 || return 0
  [ ! -e "$dir/installation-stat-sent" ] || return 0
  for attempt in 1 2 3 4 5; do
    if "$HOME_DIR/itrupanel" health-check >/dev/null 2>&1; then ready=1; break; fi
    sleep 1
  done
  [ "$ready" = 1 ] || return 0
  id=$(cat "$dir/installation-stat-id")
  version=$("$HOME_DIR/itrupanel" version)
  [[ "$id" =~ ^[a-f0-9]{64}$ && "$version" =~ ^0\.[0-9]+\.[0-9]+$ ]] || return 0
  response=$(curl --proto '=https' --connect-timeout 3 --max-time 8 -fsS \
    -H 'Content-Type: application/json' --data "{\"schema\":1,\"event\":\"installed\",\"install_id\":\"$id\",\"version\":\"$version\"}" \
    https://itrupanel.com/install-stats/api.php 2>/dev/null) || return 0
  # Mark only an explicit acknowledgement; retry uses the same random identifier.
  case "$response" in *'"ok":true'*'"recorded":true'*) : > "$dir/installation-stat-sent" ;; esac
)
