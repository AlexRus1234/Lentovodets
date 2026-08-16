// Аутентификация демона: bcrypt-логин, in-memory сессии с TTL,
// API-ключ, rate-limit на /auth/login. См. docs/SPECIFICATION.md §6.0, §9.2.

package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"lentovodec/internal/port"
)

// Лимиты аутентификации (SPECIFICATION §6.0, §9.2).
const (
	loginWindow      = 30 * time.Second // окно подсчёта попыток логина
	loginMaxAttempts = 5                // попыток на IP в окне
	sessionBytes     = 32               // 256 бит токена сессии
	maxSessions      = 1024             // лимит размера map сессий
)

// Ошибки Login; HTTP-коды им ставят обработчики.
var (
	errAuthDisabled       = errors.New("аутентификация выключена")
	errInvalidCredentials = errors.New("неверный логин или пароль")
	errRateLimited        = errors.New("слишком много попыток входа")
	errTooManySessions    = errors.New("слишком много активных сессий")
)

// session — одна активная сессия логина.
type session struct {
	username  string
	expiresAt time.Time
}

// loginAttempts — счётчик попыток логина с одного IP.
type loginAttempts struct {
	count       int
	windowStart time.Time
}

// authManager — состояние аутентификации демона. Потокобезопасен.
type authManager struct {
	mu       sync.Mutex
	log      *slog.Logger
	clock    port.Clock
	username string // "" — аутентификация выключена
	passHash string
	apiKey   string // "" — X-API-Key отключён
	ttl      time.Duration

	sessions map[string]session
	attempts map[string]*loginAttempts
}

// newAuthManager собирает менеджер аутентификации.
func newAuthManager(log *slog.Logger, clock port.Clock, username, passHash, apiKey string, ttl time.Duration) *authManager {
	return &authManager{
		log:      log,
		clock:    clock,
		username: username,
		passHash: passHash,
		apiKey:   apiKey,
		ttl:      ttl,
		sessions: make(map[string]session),
		attempts: make(map[string]*loginAttempts),
	}
}

// enabled сообщает, требуется ли аутентификация.
func (a *authManager) enabled() bool { return a.username != "" }

// Login проверяет учётные данные с rate-limit'ом по IP и создаёт
// сессию. Успешный вход сбрасывает счётчик попыток (SPEC §6.0).
func (a *authManager) Login(username, password, ip string) (string, time.Time, error) {
	if !a.enabled() {
		return "", time.Time{}, errAuthDisabled
	}
	now := a.clock.Now()

	a.mu.Lock()
	if a.tooManyAttempts(ip, now) {
		a.mu.Unlock()
		a.audit("login_failure", ip, username, "rate limit")
		return "", time.Time{}, errRateLimited
	}
	credsOK := subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) == 1 &&
		bcrypt.CompareHashAndPassword([]byte(a.passHash), []byte(password)) == nil
	if !credsOK {
		a.countAttempt(ip, now)
		a.mu.Unlock()
		a.audit("login_failure", ip, username, "неверные учётные данные")
		return "", time.Time{}, errInvalidCredentials
	}
	delete(a.attempts, ip) // успех сбрасывает счётчик
	a.purgeExpiredLocked(now)
	if len(a.sessions) >= maxSessions {
		a.mu.Unlock()
		a.audit("login_failure", ip, username, "лимит сессий")
		return "", time.Time{}, errTooManySessions
	}
	token, err := randomToken()
	if err != nil {
		a.mu.Unlock()
		return "", time.Time{}, err
	}
	expiresAt := now.Add(a.ttl)
	a.sessions[token] = session{username: username, expiresAt: expiresAt}
	a.mu.Unlock()

	a.audit("login_success", ip, username, "")
	return token, expiresAt, nil
}

// Logout удаляет сессию с токеном token.
func (a *authManager) Logout(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}

// ValidSession сообщает, что токен жив (не истёк).
func (a *authManager) ValidSession(token string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[token]
	if !ok {
		return false
	}
	if !a.clock.Now().Before(s.expiresAt) {
		delete(a.sessions, token)
		return false
	}
	return true
}

// SessionUser — имя пользователя сессии (для аудита); "" если токен
// неизвестен.
func (a *authManager) SessionUser(token string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[token].username
}

// ValidAPIKey сравнивает ключ за постоянное время; пустой настроенный
// ключ означает, что способ отключён.
func (a *authManager) ValidAPIKey(key string) bool {
	if a.apiKey == "" || key == "" {
		return false
	}
	want := sha256.Sum256([]byte(a.apiKey))
	got := sha256.Sum256([]byte(key))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

// tooManyAttempts сообщает, исчерпаны ли попытки с IP; окно
// скользящее: первая попытка после паузы в 30 c открывает новое.
// Вызывать под мьютексом.
func (a *authManager) tooManyAttempts(ip string, now time.Time) bool {
	att := a.attempts[ip]
	if att == nil {
		return false
	}
	if now.Sub(att.windowStart) >= loginWindow {
		delete(a.attempts, ip)
		return false
	}
	return att.count >= loginMaxAttempts
}

// countAttempt фиксирует неудачную попытку. Вызывать под мьютексом.
func (a *authManager) countAttempt(ip string, now time.Time) {
	att := a.attempts[ip]
	if att == nil || now.Sub(att.windowStart) >= loginWindow {
		att = &loginAttempts{windowStart: now}
		a.attempts[ip] = att
	}
	att.count++
}

// purgeExpiredLocked удаляет истёкшие сессии. Вызывать под мьютексом.
func (a *authManager) purgeExpiredLocked(now time.Time) {
	for tok, s := range a.sessions {
		if !now.Before(s.expiresAt) {
			delete(a.sessions, tok)
		}
	}
}

// audit пишет событие аутентификации в общий лог (SPEC §9.2).
func (a *authManager) audit(event, ip, username, reason string) {
	attrs := []slog.Attr{
		slog.String("event", "auth"),
		slog.String("action", event),
		slog.String("ip", ip),
		slog.String("username", username),
	}
	if reason != "" {
		attrs = append(attrs, slog.String("reason", reason))
	}
	a.log.LogAttrs(nil, slog.LevelInfo, event, attrs...)
}

// randomToken генерирует токен сессии: 256 бит из crypto/rand в hex.
func randomToken() (string, error) {
	buf := make([]byte, sessionBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.Join(errors.New("web: генерация токена сессии"), err)
	}
	return hex.EncodeToString(buf), nil
}
