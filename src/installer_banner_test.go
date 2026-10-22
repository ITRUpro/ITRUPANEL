// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func runBannerScript(t *testing.T, env []string, script string) string {
	t.Helper()
	cmd := exec.Command("bash", "-c", ". ../install-banner.sh; "+script)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "NO_COLOR=1"}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return string(out)
}

func TestInstallerShowsLogoAndPreflightChecks(t *testing.T) {
	out := runBannerScript(t, []string{"LANG=C", "LC_ALL=C"}, `itru_banner; itru_preflight "Ubuntu 24.04.5 LTS" amd64 install`)
	for _, want := range []string{"______________  __  ______  ___", `/___/ /_/ /_/ |_|\____/_/   /_/  |_/_/ |_/_____/_____/`, "Greetings!", "Start pre-installation checks", "OS:", "Ubuntu 24.04.5 LTS", "Architecture:", "amd64", "systemd:", "Memory:", "Free disk space:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatal("при NO_COLOR вывод не должен содержать цветовые коды")
	}
	// Логотип показывается один раз за запуск: повторный вызов и вызов из install.sh после онлайн-установщика молчат.
	if again := runBannerScript(t, []string{"LANG=C", "ITRUPANEL_BANNER=1"}, `itru_banner`); strings.TrimSpace(again) != "" {
		t.Fatalf("повторный показ логотипа: %q", again)
	}
}

func TestInstallerPreflightIsAlignedAndRussianByLocale(t *testing.T) {
	out := runBannerScript(t, []string{"LANG=ru_RU.UTF-8", "LC_ALL=ru_RU.UTF-8"}, `itru_preflight "Debian GNU/Linux 13" arm64 update`)
	if !strings.Contains(out, "Проверка сервера перед установкой") || !strings.Contains(out, "Архитектура:") || !strings.Contains(out, "Свободно на диске:") {
		t.Fatalf("нет русских подписей:\n%s", out)
	}
	// Значения стоят в одном столбце, даже когда подписи русские (по символам, а не по байтам).
	col := -1
	for _, line := range strings.Split(out, "\n") {
		m := regexp.MustCompile(`^(OS:|Архитектура:|root:|systemd:|Память:|Свободно на диске:|Интернет:)( +)`).FindStringSubmatch(line)
		if m == nil {
			continue
		}
		c := len([]rune(m[1])) + len(m[2])
		if col == -1 {
			col = c
		} else if c != col {
			t.Fatalf("значения не выровнены (%d и %d): %q", col, c, line)
		}
	}
	if col == -1 {
		t.Fatalf("строки проверки не найдены:\n%s", out)
	}
}

// Онлайн-установщик собирается подстановкой того же блока: проверяем, что результат рабочий и содержит логотип.
func TestBootstrapEmbedsTheSameBanner(t *testing.T) {
	banner, err := os.ReadFile("../install-banner.sh")
	if err != nil {
		t.Fatal(err)
	}
	b := string(banner)
	i, j := strings.Index(b, "# ITRUPANEL-BANNER-BEGIN"), strings.Index(b, "# ITRUPANEL-BANNER-END")
	if i < 0 || j < i {
		t.Fatal("в install-banner.sh нет меток блока")
	}
	boot, err := os.ReadFile("../release/bootstrap.sh.in")
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(string(boot), "#ITRUPANEL_BANNER_PLACEHOLDER", b[i:j], 1)
	if script == string(boot) {
		t.Fatal("в bootstrap.sh.in нет метки для логотипа")
	}
	f := t.TempDir() + "/install-itrupanel.sh"
	os.WriteFile(f, []byte(script), 0755)
	if out, err := exec.Command("bash", "-n", f).CombinedOutput(); err != nil {
		t.Fatalf("синтаксис онлайн-установщика: %v %s", err, out)
	}
	if !strings.Contains(script, "itru_banner") || !strings.Contains(script, "itru_preflight") || !strings.Contains(script, "ITRU_LOGO") {
		t.Fatal("логотип и проверка не встроены в онлайн-установщик")
	}
	inst, _ := os.ReadFile("../install.sh")
	if !strings.Contains(string(inst), `. "$HERE/install-banner.sh"`) || !strings.Contains(string(inst), "itru_preflight") {
		t.Fatal("install.sh не подключает логотип и проверку")
	}
}
