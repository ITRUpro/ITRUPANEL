// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Защита входа в панель: двухэтапная аутентификация (TOTP по RFC 6238) и доступ только с выбранных IP-адресов.
// Оба параметра необязательны и по умолчанию выключены. Если доступ потерян, их можно сбросить на сервере командами
// «itrupanel 2fa-off» и «itrupanel allow-all-ips».

const (
	totpStepSeconds = 30
	totpDigits      = 6
	recoveryCount   = 10
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() string {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return totpEncoding.EncodeToString(raw)
}

func totpAt(secret string, step int64) (string, error) {
	key, err := totpEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(key) < 10 {
		return "", errf("Неверный секрет двухэтапной аутентификации")
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, bin%mod), nil
}

// totpVerify проверяет код с допуском ±1 интервал на расхождение часов. Возвращает номер интервала, подошедшего коду.
func totpVerify(secret, code string, now time.Time) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	cur := now.Unix() / totpStepSeconds
	found := int64(-1)
	for d := int64(-1); d <= 1; d++ {
		want, err := totpAt(secret, cur+d)
		if err != nil {
			return 0, false
		}
		// Сравнение всех трёх без раннего выхода, чтобы не выдавать, какой интервал подошёл.
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			found = cur + d
		}
	}
	return found, found >= 0
}

func totpURI(account, secret string) string {
	label := url.PathEscape("ITRUPANEL:" + account)
	q := url.Values{"secret": {secret}, "issuer": {"ITRUPANEL"}, "algorithm": {"SHA1"}, "digits": {fmt.Sprint(totpDigits)}, "period": {fmt.Sprint(totpStepSeconds)}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

func hashRecovery(code string) string {
	sum := sha256.Sum256([]byte(normalizeRecovery(code)))
	return hex.EncodeToString(sum[:])
}

func normalizeRecovery(code string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

// newRecoveryCodes возвращает коды для показа (один раз) и их хеши для хранения.
func newRecoveryCodes() (plain, hashes []string) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	for i := 0; i < recoveryCount; i++ {
		raw := make([]byte, 10)
		rand.Read(raw)
		var b strings.Builder
		for j, x := range raw {
			if j == 5 {
				b.WriteByte('-')
			}
			b.WriteByte(alphabet[int(x)%len(alphabet)])
		}
		plain = append(plain, b.String())
		hashes = append(hashes, hashRecovery(b.String()))
	}
	return
}

// secondFactorOK проверяет код приложения или резервный код. Использованный код повторно не принимается.
func secondFactorOK(code string) bool {
	mu.Lock()
	defer mu.Unlock()
	s := &store.Settings
	if !s.TOTPEnabled || s.TOTPSecret == "" {
		return true
	}
	if step, ok := totpVerify(s.TOTPSecret, code, time.Now()); ok {
		if step <= s.TOTPLastStep {
			return false // этот код уже использован
		}
		s.TOTPLastStep = step
		save()
		return true
	}
	h := hashRecovery(code)
	for i, x := range s.TOTPRecovery {
		if subtle.ConstantTimeCompare([]byte(x), []byte(h)) == 1 {
			s.TOTPRecovery = append(append([]string(nil), s.TOTPRecovery[:i]...), s.TOTPRecovery[i+1:]...)
			save()
			return true
		}
	}
	return false
}

// ---------- список разрешённых адресов ----------

func parseAllowIPs(text string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\r' }) {
		var p netip.Prefix
		if strings.Contains(f, "/") {
			x, err := netip.ParsePrefix(f)
			if err != nil {
				return nil, errf("Неверный адрес или сеть: %s", f)
			}
			p = x.Masked()
		} else {
			a, err := netip.ParseAddr(f)
			if err != nil {
				return nil, errf("Неверный адрес или сеть: %s", f)
			}
			a = a.Unmap()
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Addr().Is4In6() {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		s := p.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
		if len(out) > 100 {
			return nil, errf("Слишком много адресов: не больше 100")
		}
	}
	return out, nil
}

// ipAllowed говорит, входит ли адрес в список; пустой список разрешает всех.
func ipAllowed(list []string, ip string) bool {
	if len(list) == 0 {
		return true
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, s := range list {
		if p, err := netip.ParsePrefix(s); err == nil && p.Contains(a) {
			return true
		}
	}
	return false
}

// panelIPGuard пропускает к панели только адреса из списка. Локальные подключения самого сервера
// (например, nginx без заголовка адреса посетителя) не ограничиваются.
func panelIPGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		list := store.Settings.AllowIPs
		mu.Unlock()
		if len(list) > 0 {
			ip := clientIP(r)
			if a, err := netip.ParseAddr(ip); !(err == nil && a.IsLoopback()) && !ipAllowed(list, ip) {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, "ITRUPANEL: доступ к панели с этого адреса запрещён.\nITRUPANEL: access to the panel from this address is not allowed.\n")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- программный интерфейс ----------

var pending2FA = struct {
	sync.Mutex
	secret  string
	created time.Time
}{}

func registerLoginSecurity() {
	api("security.login.get", func(q *Req) (any, error) {
		mu.Lock()
		s := store.Settings
		mu.Unlock()
		return map[string]any{
			"totp":    map[string]any{"enabled": s.TOTPEnabled, "recovery_left": len(s.TOTPRecovery)},
			"ips":     map[string]any{"list": append([]string{}, s.AllowIPs...), "current": clientIP(q.R)},
			"account": s.AdminUser,
		}, nil
	})
	apiOp("security.2fa.begin", "Двухэтапная аутентификация: настройка", func(q *Req) (any, error) {
		mu.Lock()
		enabled, account := store.Settings.TOTPEnabled, store.Settings.AdminUser
		mu.Unlock()
		if enabled {
			return nil, errf("Двухэтапная аутентификация уже включена")
		}
		secret := newTOTPSecret()
		pending2FA.Lock()
		pending2FA.secret, pending2FA.created = secret, time.Now()
		pending2FA.Unlock()
		return map[string]any{"secret": secret, "uri": totpURI(account, secret)}, nil
	})
	apiOp("security.2fa.enable", "Двухэтапная аутентификация: включение", func(q *Req) (any, error) {
		var in struct {
			Code string `json:"code"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		pending2FA.Lock()
		secret, created := pending2FA.secret, pending2FA.created
		pending2FA.Unlock()
		if secret == "" || time.Since(created) > 15*time.Minute {
			return nil, errf("Настройка устарела: начните заново")
		}
		step, ok := totpVerify(secret, in.Code, time.Now())
		if !ok {
			return nil, errf("Неверный код. Проверьте время на телефоне и повторите")
		}
		plain, hashes := newRecoveryCodes()
		mu.Lock()
		s := &store.Settings
		s.TOTPSecret, s.TOTPEnabled, s.TOTPLastStep, s.TOTPRecovery = secret, true, step, hashes
		save()
		mu.Unlock()
		pending2FA.Lock()
		pending2FA.secret = ""
		pending2FA.Unlock()
		return map[string]any{"recovery": plain}, nil
	})
	// verifyOwner подтверждает действие паролем и действующим кодом (или резервным кодом).
	verifyOwner := func(password, code string) error {
		mu.Lock()
		hash := store.Settings.AdminHash
		mu.Unlock()
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
			return errf("Неверный пароль")
		}
		if !secondFactorOK(code) {
			return errf("Неверный код")
		}
		return nil
	}
	apiOp("security.2fa.disable", "Двухэтапная аутентификация: отключение", func(q *Req) (any, error) {
		var in struct {
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if err := verifyOwner(in.Password, in.Code); err != nil {
			return nil, err
		}
		mu.Lock()
		s := &store.Settings
		s.TOTPSecret, s.TOTPEnabled, s.TOTPLastStep, s.TOTPRecovery = "", false, 0, nil
		save()
		mu.Unlock()
		return nil, nil
	})
	apiOp("security.2fa.recovery", "Двухэтапная аутентификация: новые резервные коды", func(q *Req) (any, error) {
		var in struct {
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		enabled := store.Settings.TOTPEnabled
		mu.Unlock()
		if !enabled {
			return nil, errf("Двухэтапная аутентификация не включена")
		}
		if err := verifyOwner(in.Password, in.Code); err != nil {
			return nil, err
		}
		plain, hashes := newRecoveryCodes()
		mu.Lock()
		store.Settings.TOTPRecovery = hashes
		save()
		mu.Unlock()
		return map[string]any{"recovery": plain}, nil
	})
	apiOp("security.ips.save", "Доступ к панели: разрешённые адреса", func(q *Req) (any, error) {
		var in struct {
			List string `json:"list"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		list, err := parseAllowIPs(in.List)
		if err != nil {
			return nil, err
		}
		if cur := clientIP(q.R); len(list) > 0 && !ipAllowed(list, cur) {
			if a, e := netip.ParseAddr(cur); !(e == nil && a.IsLoopback()) {
				return nil, errf("Ваш текущий адрес %s не входит в список, и вы потеряли бы доступ. Добавьте его в список", cur)
			}
		}
		mu.Lock()
		store.Settings.AllowIPs = list
		save()
		mu.Unlock()
		return nil, nil
	})
}

// Команды для консоли сервера: ими сбрасывается защита, если доступ потерян.
func cliDisable2FA() {
	mu.Lock()
	s := &store.Settings
	s.TOTPSecret, s.TOTPEnabled, s.TOTPLastStep, s.TOTPRecovery = "", false, 0, nil
	save()
	mu.Unlock()
}

func cliAllowAllIPs() {
	mu.Lock()
	store.Settings.AllowIPs = nil
	save()
	mu.Unlock()
}

var _ = net.IPv4len
