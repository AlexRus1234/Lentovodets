// Лентоводец — система резервного копирования на ленточные накопители LTO
// Copyright (C) 2026 AlexRus1234
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package tomlconfig_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lentovodec/internal/adapter/tomlconfig"
	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// validTOML — пример из SPECIFICATION §8; первый job с ключами
// в верхнем регистре, второй — в нижнем (чтение регистронезависимо).
const validTOML = `
db    = "/var/lib/lentovodec/catalog.db"
device = "/dev/nst1"
log   = "/var/log/lentovodec.log"
server = "http://192.168.1.10:29201"
log_level = "debug"
bind = "192.168.1.10:29201"
web_username = "admin"
web_password_hash = "$2a$10$secret"
api_key = "script-key"
session_ttl = "1h"
capacity = "2.2T"
min_tail = "100G"

[[jobs]]
Name = "media"
Description = "Бекап сериалов"
Mode = "append"
Paths = ["/tank/data/media"]
Exclude = ["**/.DS_Store", "**/*.partial"]

[[jobs]]
name = "system"
description = "Системные файлы"
mode = "mirror"
paths = ["/etc", "/home"]
exclude = ["/home/*/.cache/**"]
`

// newConfig пишет toml во временный файл (если непуст) и открывает Config.
func newConfig(t *testing.T, toml string) (*tomlconfig.Config, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lentovodec.toml")
	if toml != "" {
		if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
			t.Fatalf("запись TOML: %v", err)
		}
	}
	cfg, err := tomlconfig.New(path, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return cfg, path
}

// reopen перечитывает конфиг с диска.
func reopen(t *testing.T, path string) *tomlconfig.Config {
	t.Helper()
	cfg, err := tomlconfig.New(path, nil)
	if err != nil {
		t.Fatalf("New(reopen): %v", err)
	}
	return cfg
}

func mediaJob() domain.Job {
	return domain.Job{
		Name:        "media",
		Description: "Бекап сериалов",
		Mode:        domain.ModeAppend,
		Paths:       []string{"/tank/data/media"},
		Exclude:     []string{"**/.DS_Store"},
	}
}

// jobsEqual сравнивает задания целиком; nil и пустой слайс равны
// (viper при записи нормализует отсутствующий список в []).
func jobsEqual(a, b domain.Job) bool {
	return a.Name == b.Name && a.Description == b.Description && a.Mode == b.Mode &&
		a.SpanDepth == b.SpanDepth &&
		stringsEqual(a.Paths, b.Paths) && stringsEqual(a.Exclude, b.Exclude)
}

// stringsEqual — поэлементное сравнение, nil == [].
func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestConfig_ImplementsPorts(t *testing.T) {
	cfg, _ := newConfig(t, validTOML)
	var source port.ConfigSource = cfg
	var editor port.ConfigEditor = cfg
	if source.Device() != cfg.Device() {
		t.Fatal("port.ConfigSource и конкретный тип расходятся")
	}
	if err := editor.RemoveJob("definitely-missing-job"); err == nil {
		t.Fatal("RemoveJob отсутствующего = nil, want ошибка")
	}
}

func TestNew_MissingFile_Defaults(t *testing.T) {
	cfg, _ := newConfig(t, "")

	if cfg.Device() != "/dev/nst0" {
		t.Errorf("Device = %q, want /dev/nst0", cfg.Device())
	}
	if cfg.DB() != "lentovodec.db" {
		t.Errorf("DB = %q, want lentovodec.db", cfg.DB())
	}
	if cfg.Log() != "lentovodec.log" {
		t.Errorf("Log = %q, want lentovodec.log", cfg.Log())
	}
	if cfg.Server() != "http://127.0.0.1:29201" {
		t.Errorf("Server = %q, want http://127.0.0.1:29201", cfg.Server())
	}
	if cfg.LogLevel() != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel())
	}
	jobs, err := cfg.Jobs()
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("Jobs len = %d, want 0", len(jobs))
	}
}

func TestNew_ReadsTOML_CaseInsensitive(t *testing.T) {
	cfg, _ := newConfig(t, validTOML)

	if cfg.Device() != "/dev/nst1" {
		t.Errorf("Device = %q, want /dev/nst1", cfg.Device())
	}
	if cfg.DB() != "/var/lib/lentovodec/catalog.db" {
		t.Errorf("DB = %q", cfg.DB())
	}
	if cfg.LogLevel() != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.LogLevel())
	}
	if cfg.Server() != "http://192.168.1.10:29201" {
		t.Errorf("Server = %q", cfg.Server())
	}

	jobs, err := cfg.Jobs()
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("Jobs len = %d, want 2", len(jobs))
	}
	// Первый — с ключами в верхнем регистре (как в примере SPEC §8).
	want := mediaJob()
	want.Exclude = []string{"**/.DS_Store", "**/*.partial"}
	if !jobsEqual(jobs[0], want) {
		t.Errorf("jobs[0] = %+v, want %+v", jobs[0], want)
	}
	// Второй — в нижнем; viper регистронезависим.
	if jobs[1].Name != "system" || jobs[1].Mode != domain.ModeMirror {
		t.Errorf("jobs[1] = %+v, want system/mirror", jobs[1])
	}
	if len(jobs[1].Paths) != 2 || jobs[1].Paths[0] != "/etc" {
		t.Errorf("jobs[1].Paths = %v", jobs[1].Paths)
	}
}

func TestNew_MalformedTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lentovodec.toml")
	if err := os.WriteFile(path, []byte("device = без-кавычек-и-не-число"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tomlconfig.New(path, nil); err == nil {
		t.Fatal("New на битом TOML = nil, want ошибка разбора")
	}
}

func TestLayers_EnvOverridesFile(t *testing.T) {
	t.Setenv("LENTOVODEC_DEVICE", "/dev/nst2")
	t.Setenv("LENTOVODEC_LOG_LEVEL", "warn")
	cfg, _ := newConfig(t, validTOML)

	if cfg.Device() != "/dev/nst2" {
		t.Errorf("Device = %q, want /dev/nst2 (env бьёт файл)", cfg.Device())
	}
	if cfg.LogLevel() != "warn" {
		t.Errorf("LogLevel = %q, want warn", cfg.LogLevel())
	}
	if cfg.DB() != "/var/lib/lentovodec/catalog.db" {
		t.Errorf("DB = %q, want из файла", cfg.DB())
	}
}

func TestLayers_FlagsOverrideEnv(t *testing.T) {
	t.Setenv("LENTOVODEC_DEVICE", "/dev/nst2")
	path := filepath.Join(t.TempDir(), "lentovodec.toml")
	if err := os.WriteFile(path, []byte(validTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := tomlconfig.New(path, map[string]string{"device": "/dev/nst9"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if cfg.Device() != "/dev/nst9" {
		t.Errorf("Device = %q, want /dev/nst9 (флаг бьёт env)", cfg.Device())
	}
}

func TestLayers_EmptyFlagIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lentovodec.toml")
	if err := os.WriteFile(path, []byte(validTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := tomlconfig.New(path, map[string]string{"device": "", "db": ""})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if cfg.Device() != "/dev/nst1" {
		t.Errorf("Device = %q, want /dev/nst1 из файла (пустой флаг игнорируется)", cfg.Device())
	}
}

func TestJobs_TypeMismatch(t *testing.T) {
	cfg, _ := newConfig(t, "jobs = 42\n")
	if _, err := cfg.Jobs(); err == nil {
		t.Fatal("Jobs на битой секции = nil, want ошибка разбора")
	}
}

func TestAddJob_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lentovodec.toml")
	cfg, err := tomlconfig.New(path, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := cfg.AddJob(mediaJob()); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	reopened := reopen(t, path)
	jobs, err := reopened.Jobs()
	if err != nil {
		t.Fatalf("Jobs(reopen): %v", err)
	}
	if len(jobs) != 1 || !jobsEqual(jobs[0], mediaJob()) {
		t.Fatalf("jobs = %+v, want round-trip %+v", jobs, mediaJob())
	}
	if reopened.DB() != "lentovodec.db" {
		t.Errorf("DB = %q, want default (в новом файле только jobs)", reopened.DB())
	}
}

func TestAddJob_AppendsAndPreservesOtherKeys(t *testing.T) {
	cfg, path := newConfig(t, validTOML)
	extra := domain.Job{
		Name:  "photos",
		Mode:  domain.ModeMirror,
		Paths: []string{"/photos"},
	}
	if err := cfg.AddJob(extra); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "web_password_hash") || !strings.Contains(text, "$2a$10$secret") {
		t.Errorf("секрет потерян при записи:\n%s", text)
	}
	if !strings.Contains(text, "/var/lib/lentovodec/catalog.db") {
		t.Errorf("db потерян при записи:\n%s", text)
	}

	reopened := reopen(t, path)
	jobs, err := reopened.Jobs()
	if err != nil {
		t.Fatalf("Jobs(reopen): %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("jobs len = %d, want 3", len(jobs))
	}
	if !jobsEqual(jobs[2], extra) {
		t.Errorf("jobs[2] = %+v, want %+v", jobs[2], extra)
	}
	if reopened.Device() != "/dev/nst1" {
		t.Errorf("Device после записи = %q, want /dev/nst1", reopened.Device())
	}
}

func TestAddJob_VisibleWithoutReopen(t *testing.T) {
	cfg, _ := newConfig(t, validTOML)
	if err := cfg.AddJob(domain.Job{Name: "photos", Mode: domain.ModeAppend, Paths: []string{"/photos"}}); err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	jobs, err := cfg.Jobs()
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("jobs len = %d, want 3 (обновилось in-memory без перечитывания файла)", len(jobs))
	}
	if jobs[2].Name != "photos" {
		t.Errorf("jobs[2].Name = %q, want photos", jobs[2].Name)
	}
}

func TestAddJob_DuplicateRejected(t *testing.T) {
	cfg, path := newConfig(t, validTOML)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddJob(mediaJob()); err == nil {
		t.Fatal("дубликат имени принят, want ошибка")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("файл изменился при отклонённом AddJob")
	}
}

func TestAddJob_InvalidJobRejected(t *testing.T) {
	cfg, path := newConfig(t, validTOML)
	bad := mediaJob()
	bad.Mode = "синхронизация"
	if err := cfg.AddJob(bad); err == nil {
		t.Fatal("невалидное задание принято, want ошибка Validate")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	jobs, err := cfg.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Errorf("jobs len = %d, want 2 (невалидное не добавлено)", len(jobs))
	}
}

// TestJobs_SpanDepthPerJob — ключ span_depth читается per-job;
// отсутствие ключа — дефолт 0 (резка по файлам).
func TestJobs_SpanDepthPerJob(t *testing.T) {
	cfg, _ := newConfig(t, `
[[jobs]]
name = "media"
mode = "append"
paths = ["/tank/data/media"]
span_depth = 2

[[jobs]]
name = "system"
mode = "mirror"
paths = ["/etc"]
`)
	jobs, err := cfg.Jobs()
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("jobs len = %d, want 2", len(jobs))
	}
	if jobs[0].SpanDepth != 2 {
		t.Errorf("jobs[0].SpanDepth = %d, want 2", jobs[0].SpanDepth)
	}
	if jobs[1].SpanDepth != 0 {
		t.Errorf("jobs[1].SpanDepth = %d, want 0 (дефолт без ключа)", jobs[1].SpanDepth)
	}
}

// TestAddJob_SpanDepthRoundTrip — span_depth переживает запись в TOML
// и перечитывание; нулевая глубина не пишется (чистый конфиг).
func TestAddJob_SpanDepthRoundTrip(t *testing.T) {
	cfg, path := newConfig(t, "")
	grouped := mediaJob()
	grouped.SpanDepth = 1
	if err := cfg.AddJob(grouped); err != nil {
		t.Fatalf("AddJob(span_depth=1): %v", err)
	}
	if err := cfg.AddJob(domain.Job{Name: "plain", Mode: domain.ModeAppend, Paths: []string{"/etc"}}); err != nil {
		t.Fatalf("AddJob(span_depth=0): %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "span_depth") {
		t.Errorf("span_depth не записан в TOML:\n%s", raw)
	}

	reopened := reopen(t, path)
	jobs, err := reopened.Jobs()
	if err != nil {
		t.Fatalf("Jobs(reopen): %v", err)
	}
	if len(jobs) != 2 || !jobsEqual(jobs[0], grouped) {
		t.Fatalf("jobs[0] = %+v, want round-trip %+v", jobs[0], grouped)
	}
	if jobs[1].SpanDepth != 0 {
		t.Errorf("jobs[1].SpanDepth = %d, want 0 (не писалась)", jobs[1].SpanDepth)
	}
}

// TestAddJob_NegativeSpanDepthRejected — отрицательная глубина —
// ошибка валидации задания, файл не меняется.
func TestAddJob_NegativeSpanDepthRejected(t *testing.T) {
	cfg, _ := newConfig(t, validTOML)
	bad := mediaJob()
	bad.SpanDepth = -3
	if err := cfg.AddJob(bad); err == nil {
		t.Fatal("отрицательная span_depth принята, want ошибка Validate")
	}
	jobs, err := cfg.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].SpanDepth != 0 {
		t.Fatalf("jobs = %+v, want 2 исходных без span_depth", jobs)
	}
}

// TestRemoveJob_PreservesSpanDepth — перезапись [[jobs]] при удалении
// сохраняет span_depth оставшихся заданий.
func TestRemoveJob_PreservesSpanDepth(t *testing.T) {
	cfg, path := newConfig(t, `
[[jobs]]
name = "media"
mode = "append"
paths = ["/tank/data/media"]
span_depth = 1

[[jobs]]
name = "system"
mode = "mirror"
paths = ["/etc"]
`)
	if err := cfg.RemoveJob("system"); err != nil {
		t.Fatalf("RemoveJob: %v", err)
	}
	reopened := reopen(t, path)
	jobs, err := reopened.Jobs()
	if err != nil {
		t.Fatalf("Jobs(reopen): %v", err)
	}
	if len(jobs) != 1 || jobs[0].SpanDepth != 1 {
		t.Fatalf("jobs = %+v, want media с span_depth=1", jobs)
	}
}

func TestRemoveJob(t *testing.T) {
	cfg, path := newConfig(t, validTOML)
	if err := cfg.RemoveJob("media"); err != nil {
		t.Fatalf("RemoveJob: %v", err)
	}

	reopened := reopen(t, path)
	jobs, err := reopened.Jobs()
	if err != nil {
		t.Fatalf("Jobs(reopen): %v", err)
	}
	if len(jobs) != 1 || jobs[0].Name != "system" {
		t.Fatalf("jobs = %+v, want только system", jobs)
	}
}

func TestRemoveJob_LastLeavesEmptyList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lentovodec.toml")
	cfg, err := tomlconfig.New(path, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := cfg.AddJob(mediaJob()); err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	if err := cfg.RemoveJob("media"); err != nil {
		t.Fatalf("RemoveJob: %v", err)
	}

	reopened := reopen(t, path)
	jobs, err := reopened.Jobs()
	if err != nil {
		t.Fatalf("Jobs(reopen): %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("jobs len = %d, want 0", len(jobs))
	}
}

func TestRemoveJob_Missing(t *testing.T) {
	cfg, _ := newConfig(t, validTOML)
	if err := cfg.RemoveJob("нет-такого"); err == nil {
		t.Fatal("RemoveJob отсутствующего = nil, want ошибка")
	}
	jobs, err := cfg.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Errorf("jobs len = %d, want 2", len(jobs))
	}
}

func TestAddJob_WriteFailure(t *testing.T) {
	// Родительский каталог не существует: чтение проходит (файла нет),
	// запись — нет.
	path := filepath.Join(t.TempDir(), "нет-каталога", "lentovodec.toml")
	cfg, err := tomlconfig.New(path, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := cfg.AddJob(mediaJob()); err == nil {
		t.Fatal("AddJob в недоступный путь = nil, want ошибка записи")
	}
	jobs, err := cfg.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Errorf("после сбоя записи jobs len = %d, want 0 (память консистентна диску)", len(jobs))
	}
}

func TestWebKeys_ReadAndDefaults(t *testing.T) {
	cfg, _ := newConfig(t, validTOML)
	if cfg.Bind() != "192.168.1.10:29201" {
		t.Errorf("Bind = %q, want 192.168.1.10:29201", cfg.Bind())
	}
	if cfg.WebUsername() != "admin" {
		t.Errorf("WebUsername = %q, want admin", cfg.WebUsername())
	}
	if cfg.WebPasswordHash() != "$2a$10$secret" {
		t.Errorf("WebPasswordHash = %q, want $2a$10$secret", cfg.WebPasswordHash())
	}
	if cfg.APIKey() != "script-key" {
		t.Errorf("APIKey = %q, want script-key", cfg.APIKey())
	}
	if got, want := cfg.SessionTTL(), time.Hour; got != want {
		t.Errorf("SessionTTL = %v, want %v", got, want)
	}
	webhook, _ := newConfig(t, `webhook_url = "https://ntfy.example/topic"
webhook_timeout = "3s"`)
	if webhook.WebhookURL() != "https://ntfy.example/topic" || webhook.WebhookTimeout() != 3*time.Second {
		t.Errorf("webhook config = %q, %v", webhook.WebhookURL(), webhook.WebhookTimeout())
	}

	def, _ := newConfig(t, "")
	if def.Bind() != "127.0.0.1:29201" {
		t.Errorf("Bind default = %q, want 127.0.0.1:29201", def.Bind())
	}
	if def.WebUsername() != "" || def.WebPasswordHash() != "" || def.APIKey() != "" {
		t.Error("секретные ключи без файла должны быть пустыми")
	}
	if got, want := def.SessionTTL(), 72*time.Hour; got != want {
		t.Errorf("SessionTTL default = %v, want %v", got, want)
	}
	if def.WebhookURL() != "" || def.WebhookTimeout() != 10*time.Second {
		t.Errorf("webhook defaults = %q, %v", def.WebhookURL(), def.WebhookTimeout())
	}
}

func TestSessionTTL_InvalidFallsBack(t *testing.T) {
	cfg, _ := newConfig(t, `session_ttl = "очень-долго"`)
	if got, want := cfg.SessionTTL(), 72*time.Hour; got != want {
		t.Errorf("SessionTTL = %v, want fallback %v", got, want)
	}
}

func TestSpanKeys_ReadTOML(t *testing.T) {
	cfg, _ := newConfig(t, validTOML)
	capacity, err := cfg.Capacity()
	if err != nil {
		t.Fatalf("Capacity: %v", err)
	}
	if capacity != 2418925581107 { // 2.2 * 2^40
		t.Errorf("Capacity = %d; want 2418925581107", capacity)
	}
	tail, err := cfg.MinTail()
	if err != nil {
		t.Fatalf("MinTail: %v", err)
	}
	if tail != 107374182400 { // 100 * 2^30
		t.Errorf("MinTail = %d; want 107374182400", tail)
	}
}

func TestSpanKeys_DefaultsZero(t *testing.T) {
	cfg, _ := newConfig(t, "")
	capacity, err := cfg.Capacity()
	if capacity != 0 || err != nil {
		t.Errorf("Capacity = %d, %v; want 0, nil (spanning выключен)", capacity, err)
	}
	tail, err := cfg.MinTail()
	if tail != 0 || err != nil {
		t.Errorf("MinTail = %d, %v; want 0, nil без capacity", tail, err)
	}
}

func TestMinTail_DefaultFivePercent(t *testing.T) {
	cfg, _ := newConfig(t, `capacity = "1T"`)
	tail, err := cfg.MinTail()
	if err != nil {
		t.Fatalf("MinTail: %v", err)
	}
	if tail != 54975581388 { // 5% от 2^40
		t.Errorf("MinTail = %d; want 54975581388", tail)
	}
	// маленькая ёмкость: 5% меньше блока ленты — зажимается блоком
	small, _ := newConfig(t, `capacity = "1M"`)
	tail, err = small.MinTail()
	if err != nil {
		t.Fatalf("MinTail(1M): %v", err)
	}
	if tail != domain.BlockSize {
		t.Errorf("MinTail(1M) = %d; want domain.BlockSize", tail)
	}
}

func TestSpanKeys_EnvAndFlags(t *testing.T) {
	t.Setenv("LENTOVODEC_CAPACITY", "3G")
	t.Setenv("LENTOVODEC_MIN_TAIL", "1G")
	cfg, _ := newConfig(t, validTOML)
	if capacity, err := cfg.Capacity(); err != nil || capacity != 3221225472 {
		t.Errorf("Capacity = %d, %v; want 3G из env", capacity, err)
	}
	if tail, err := cfg.MinTail(); err != nil || tail != 1073741824 {
		t.Errorf("MinTail = %d, %v; want 1G из env", tail, err)
	}

	path := filepath.Join(t.TempDir(), "lentovodec.toml")
	if err := os.WriteFile(path, []byte(validTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	flagged, err := tomlconfig.New(path, map[string]string{"capacity": "512M"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if capacity, err := flagged.Capacity(); err != nil || capacity != 512<<20 {
		t.Errorf("Capacity = %d, %v; want 512M из флага", capacity, err)
	}
}

func TestSpanKeys_Invalid(t *testing.T) {
	cfg, _ := newConfig(t, `capacity = "много"`)
	if _, err := cfg.Capacity(); err == nil {
		t.Error("Capacity на битой строке = nil, want ошибка")
	}
	if _, err := cfg.MinTail(); err == nil {
		t.Error("MinTail с битым capacity = nil, want ошибка")
	}

	badTail, _ := newConfig(t, "capacity = \"1G\"\nmin_tail = \"чуть-чуть\"\n")
	if _, err := badTail.Capacity(); err != nil {
		t.Fatalf("Capacity: %v", err)
	}
	if _, err := badTail.MinTail(); err == nil {
		t.Error("MinTail на битой строке = nil, want ошибка")
	}
}

func TestSecrets_EnvDoesNotLeak(t *testing.T) {
	// SPECIFICATION §8: секреты через env не передаются — env-переменные
	// не должны ни перекрыть TOML, ни создать значение из ничего.
	t.Setenv("LENTOVODEC_API_KEY", "env-key")
	t.Setenv("LENTOVODEC_WEB_PASSWORD_HASH", "env-hash")

	cfg, _ := newConfig(t, validTOML)
	if cfg.APIKey() != "script-key" {
		t.Errorf("APIKey = %q, want из файла (env проигнорирован)", cfg.APIKey())
	}
	if cfg.WebPasswordHash() != "$2a$10$secret" {
		t.Errorf("WebPasswordHash = %q, want из файла", cfg.WebPasswordHash())
	}

	empty, _ := newConfig(t, "")
	if empty.APIKey() != "" || empty.WebPasswordHash() != "" {
		t.Error("env не должен создавать секреты там, где файла нет")
	}
}

func TestRawTOML(t *testing.T) {
	cfg, _ := newConfig(t, validTOML)
	raw, err := cfg.RawTOML()
	if err != nil {
		t.Fatalf("RawTOML: %v", err)
	}
	if !strings.Contains(raw, `device = "/dev/nst1"`) {
		t.Errorf("RawTOML не содержит device:\n%s", raw)
	}

	cfgMissing, _ := newConfig(t, "")
	raw, err = cfgMissing.RawTOML()
	if err != nil {
		t.Fatalf("RawTOML без файла: %v", err)
	}
	if raw != "" {
		t.Errorf("RawTOML без файла = %q, want пусто", raw)
	}
}
