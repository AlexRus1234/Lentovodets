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

// covercheck сверяет покрытие из профиля `go test -coverprofile`
// с порогами docs/TESTING.md §4; выход 1 — какой-то пакет ниже порога.
// Использование: go run ./tools/covercheck -profile coverage/coverage.out
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// counters — накопитель покрытия одного префикса.
type counters struct {
	total   int
	covered int
}

// thresholds — пороги TESTING.md §4: префикс каталога → минимум
// покрытия в процентах. Итог по internal/ — самый общий префикс,
// сверяется вместе с остальными.
func thresholds() map[string]float64 {
	return map[string]float64{
		"internal/domain":             100,
		"internal/usecase":            95,
		"internal/adapter/tapeformat": 100,
		"internal/adapter/osfs":       90,
		"internal/adapter/sqlite":     90,
		"internal/adapter/tomlconfig": 90,
		"internal/adapter/filetape":   95,
		"internal/iface/cli":          60,
		"internal/iface/web":          70,
		"internal":                    90,
	}
}

// informational — префиксы без порога: internal/testutil — тестовые
// двойники, не продуктивный код; в таблице §4 их нет, в итог и отчёт
// они не входят.
func informational() []string {
	return []string{"internal/testutil"}
}

func main() {
	profile := flag.String("profile", "coverage/coverage.out", "файл профиля go test -coverprofile")
	flag.Parse()
	if err := run(*profile); err != nil {
		fmt.Fprintln(os.Stderr, "covercheck:", err)
		os.Exit(1)
	}
}

// run считает покрытие по префиксам и сверяет с порогами.
func run(profile string) error {
	limits := thresholds()
	stats := make(map[string]*counters, len(limits))
	if err := parseProfile(profile, stats, informational()); err != nil {
		return err
	}

	// сначала конкретные каталоги, затем сводные префиксы
	prefixes := make([]string, 0, len(limits))
	for prefix := range limits {
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		return strings.Count(prefixes[i], "/") > strings.Count(prefixes[j], "/")
	})

	failed := false
	for _, prefix := range prefixes {
		c := stats[prefix]
		if c == nil || c.total == 0 {
			fmt.Printf("%-32s нет данных в профиле\n", prefix)
			failed = true
			continue
		}
		got := float64(c.covered) / float64(c.total) * 100
		status := "ok"
		if got < limits[prefix] {
			status = "НИЖЕ ПОРОГА"
			failed = true
		}
		fmt.Printf("%-32s %5.1f%% (порог %.0f%%) %s\n", prefix, got, limits[prefix], status)
	}
	if failed {
		return fmt.Errorf("покрытие ниже порогов TESTING.md §4")
	}
	return nil
}

// parseProfile читет профиль coverprofile, накапливая утверждения
// по совпавшим префиксам порогов; skip — исключаемые префиксы.
func parseProfile(profile string, stats map[string]*counters, skip []string) error {
	f, err := os.Open(profile)
	if err != nil {
		return fmt.Errorf("открытие %s: %w", profile, err)
	}
	defer func() { _ = f.Close() }()

	limits := thresholds()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return fmt.Errorf("строка профиля не разобрана: %q", line)
		}
		stmts, err := strconv.Atoi(fields[1])
		if err != nil {
			return fmt.Errorf("число утверждений в %q: %w", line, err)
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			return fmt.Errorf("счётчик в %q: %w", line, err)
		}
		rel := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(fields[0])), "lentovodec/")
		if matchesAny(rel, skip) {
			continue
		}
		for prefix := range limits {
			if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
				add(stats, prefix, stmts, count)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("чтение %s: %w", profile, err)
	}
	return nil
}

// matchesAny сообщает, что rel — сам prefix или лежит под одним из
// префиксов списка.
func matchesAny(rel string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
			return true
		}
	}
	return false
}

// add накапливает утверждение профиля в счётчик префикса.
func add(stats map[string]*counters, prefix string, stmts, count int) {
	c := stats[prefix]
	if c == nil {
		c = &counters{}
		stats[prefix] = c
	}
	c.total += stmts
	if count > 0 {
		c.covered += stmts
	}
}
