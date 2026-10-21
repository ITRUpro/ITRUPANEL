// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Автоматическое обновление панели. По умолчанию выключено: администратор по-прежнему обновляет панель
// вручную кнопкой «Обновления». Если включено, раз в сутки в выбранный час панель проверяет подписанный
// выпуск и ставит его тем же способом, что и вручную (проверка подписи и SHA-256, копия настроек, откат).

const (
	autoUpdateOff   = "off"
	autoUpdatePatch = "patch" // только исправления внутри текущей линии: 0.2.x → 0.2.y
	autoUpdateAll   = "all"   // любые новые выпуски
	autoUpdateWin   = 3       // окно попыток в часах: если панель была занята, пробуем позже
)

var autoUpd struct {
	sync.Mutex
	day     string // день, в который попытка уже сделана
	runID   string // обновление, запущенное автоматически
	version string
}

func normalizeAutoUpdate(mode string, hour int) (string, int, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = autoUpdateOff
	}
	if mode != autoUpdateOff && mode != autoUpdatePatch && mode != autoUpdateAll {
		return "", 0, errf("Неверный режим автоматического обновления")
	}
	if hour < 0 || hour > 23 {
		return "", 0, errf("Час от 0 до 23")
	}
	return mode, hour, nil
}

// autoUpdateAllowed говорит, подходит ли выпуск выбранному режиму.
func autoUpdateAllowed(mode, current, candidate string) bool {
	switch mode {
	case autoUpdateAll:
		return true
	case autoUpdatePatch:
		a, b := strings.Split(current, "."), strings.Split(candidate, ".")
		return len(a) == 3 && len(b) == 3 && a[0] == b[0] && a[1] == b[1]
	}
	return false
}

// panelAutoUpdateTick вызывается периодически (раз в несколько минут) из фонового цикла панели.
func panelAutoUpdateTick(now time.Time) {
	if cfg.Dry {
		return
	}
	mu.Lock()
	mode, hour := store.Settings.AutoUpdate, store.Settings.AutoUpdateHour
	mu.Unlock()
	autoUpd.Lock()
	defer autoUpd.Unlock()
	// Итог прошлого автоматического обновления: сообщаем один раз.
	if run := reconcilePanelUpdate(); run != nil && run.Auto {
		reportAutoUpdateResult(run)
		// Подготовленное обновление применяем сами: администратор включил автоматический режим.
		if run.State == "ready" && mode != "" && mode != autoUpdateOff {
			if _, err := applyPanelUpdate(run.ID); err != nil {
				mu.Lock()
				addNotice("warn", "autoupdate-apply", fmt.Sprintf("Автоматическое обновление панели не применено: %s", firstLine(err.Error())))
				save()
				mu.Unlock()
			}
			return
		}
	}
	if mode == "" || mode == autoUpdateOff {
		return
	}
	if (now.Hour()-hour+24)%24 >= autoUpdateWin {
		return
	}
	day := now.Format("2006-01-02")
	if autoUpd.day == day || runningPanelUpdate(reconcilePanelUpdate()) {
		return
	}
	r, _, errText := cachedPanelRelease(true)
	if errText != "" || r == nil {
		return // сеть недоступна: попробуем при следующей проверке
	}
	cmp, e := compareVersions(r.Version, Version)
	if e != nil || cmp <= 0 {
		autoUpd.day = day
		return
	}
	if !autoUpdateAllowed(mode, Version, r.Version) {
		autoUpd.day = day
		mu.Lock()
		addNotice("info", "autoupdate-"+r.Version, fmt.Sprintf("Вышла версия %s. Автоматическое обновление выбрано только для исправлений, поэтому обновите панель вручную (кнопка «Обновления»).", r.Version))
		save()
		mu.Unlock()
		return
	}
	run, err := startPanelUpdateAuto(r.Version, r.SHA256)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "Дождитесь") || strings.Contains(msg, "уже выполняется") {
			return // панель занята задачами: повторим позже в пределах окна
		}
		autoUpd.day = day
		mu.Lock()
		addNotice("warn", "autoupdate-error-"+r.Version, fmt.Sprintf("Автоматическое обновление панели не удалось: %s", firstLine(msg)))
		save()
		mu.Unlock()
		return
	}
	autoUpd.day = day
	autoUpd.runID, autoUpd.version = run.ID, r.Version
	mu.Lock()
	addNotice("info", "autoupdate-start-"+r.Version, fmt.Sprintf("Начато автоматическое обновление панели до версии %s", r.Version))
	save()
	mu.Unlock()
}

// reportAutoUpdateResult сообщает об итоге автоматического обновления один раз (по номеру запуска).
func reportAutoUpdateResult(run *PanelUpdateRun) {
	if run.State != "done" && run.State != "error" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if store.Settings.AutoUpdateNotified == run.ID {
		return
	}
	store.Settings.AutoUpdateNotified = run.ID
	if run.State == "done" {
		addNotice("info", "", fmt.Sprintf("Панель автоматически обновлена до версии %s", run.Version))
	} else {
		addNotice("error", "", fmt.Sprintf("Автоматическое обновление панели до версии %s не завершено: %s. Панель продолжает работать на прежней версии.", run.Version, firstLine(run.Error)))
	}
	save()
}

func registerPanelAutoUpdate() {
	api("updates.auto.get", func(q *Req) (any, error) {
		mu.Lock()
		mode, hour := store.Settings.AutoUpdate, store.Settings.AutoUpdateHour
		mu.Unlock()
		if mode == "" {
			mode = autoUpdateOff
		}
		return map[string]any{"mode": mode, "hour": hour, "version": Version, "demo": cfg.Dry}, nil
	})
	apiOp("updates.auto.save", "Автоматическое обновление панели", func(q *Req) (any, error) {
		var in struct {
			Mode string `json:"mode"`
			Hour int    `json:"hour"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mode, hour, err := normalizeAutoUpdate(in.Mode, in.Hour)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		store.Settings.AutoUpdate, store.Settings.AutoUpdateHour = mode, hour
		save()
		mu.Unlock()
		autoUpd.Lock()
		autoUpd.day = "" // новый выбор: сегодняшнее окно ещё не использовано
		autoUpd.Unlock()
		return nil, nil
	})
}
