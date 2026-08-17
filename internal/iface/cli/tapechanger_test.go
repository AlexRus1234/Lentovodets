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

// Тесты смены кассет CLI: интерактивный промпт и флаг --next-tape.

package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

// spanTOML — задание media с ёмкостью кассеты 100 байт: два файла
// 60+50 байт режутся на две части. capacity обязан стоять до
// [[jobs]]: в TOML ключи после заголовка таблицы принадлежат ей.
const spanTOML = `
capacity = "100B"
` + jobTOML

// seedSpanFiles наполняет ФС файлами 60 и 50 байт.
func seedSpanFiles(t *testing.T, e *env) {
	t.Helper()
	fs := e.deps.FS
	if err := fs.MkdirAll("/data", 0o755); err != nil {
		t.Fatal(err)
	}
	for p, n := range map[string]int{"/data/a.txt": 60, "/data/b.txt": 50} {
		wc, err := fs.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wc.Write([]byte(strings.Repeat("x", n))); err != nil {
			t.Fatal(err)
		}
		if err := wc.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// spanEnv — окружение с двумя файлами, зарегистрированной кассетой
// и раздельными UUID запуска и второй кассеты. OpenTape раздаёт:
// первый вызов — кассету T1, дальше — чистые ленты (оператор
// вставил новую кассету).
func spanEnv(t *testing.T, interactive bool, stdin string) *env {
	t.Helper()
	e := newEnv(t, spanTOML, nil)
	e.deps.Stdin = strings.NewReader(stdin)
	e.deps.IsInteractive = func() bool { return interactive }
	e.deps.Rand = testutil.FixedRand("run-uuid-1", "tape-2-uuid")
	calls := 0
	e.deps.OpenTape = func(string) (port.Tape, error) {
		calls++
		if calls == 1 {
			return tapeFor(e.codec), nil
		}
		return testutil.NewFakeTape(), nil
	}
	registerTape(t, e.cat)
	seedSpanFiles(t, e)
	return e
}

// TestBackup_SpanningPrompt — интерактивная смена кассеты: промпт
// с предложенным именем (T1 → T2), Enter принимает предложение,
// вторая кассета форматируется и регистрируется, цепочка в каталоге.
func TestBackup_SpanningPrompt(t *testing.T) {
	e := spanEnv(t, true, "\n")
	out, err := outOf(t, e, "backup", "media")
	if err != nil {
		t.Fatalf("backup: %v (stderr: %s)", err, e.deps.Stderr.(*bytes.Buffer).String())
	}
	if !strings.Contains(out, "частей 2 на кассетах: T1, T2") {
		t.Fatalf("вывод: %q", out)
	}
	stderr := e.deps.Stderr.(*bytes.Buffer).String()
	if !strings.Contains(stderr, "Кассета T1 закрыта. Вставьте чистую кассету, имя [T2]:") {
		t.Errorf("промпт не напечатан: %q", stderr)
	}
	ctx := context.Background()
	tapes, err := e.cat.ListTapes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tapes) != 2 {
		t.Fatalf("кассет в каталоге %d; want 2: %+v", len(tapes), tapes)
	}
	sessions, _ := e.cat.ListSessions(ctx, "")
	if len(sessions) != 2 {
		t.Fatalf("сессий %d; want 2", len(sessions))
	}
	for _, s := range sessions {
		if s.JobRunID != "run-uuid-1" {
			t.Errorf("сессия: %+v; JobRunID want run-uuid-1", s)
		}
	}
	chain, _ := e.cat.GetSessionChain(ctx, "run-uuid-1")
	if len(chain) != 2 || chain[1].TapeUUID != "tape-2-uuid" ||
		chain[1].Num != 1 || chain[1].Part != 2 {
		t.Errorf("цепочка: %+v", chain)
	}
}

// TestBackup_SpanningNextTapeFlag — неинтерактивный режим с флагом
// --next-tape: кассета берётся без вопросов.
func TestBackup_SpanningNextTapeFlag(t *testing.T) {
	e := spanEnv(t, false, "")
	out, err := outOf(t, e, "backup", "--next-tape", "media-009", "media")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if !strings.Contains(out, "частей 2 на кассетах: T1, media-009") {
		t.Fatalf("вывод: %q", out)
	}
	tapes, _ := e.cat.ListTapes(context.Background())
	if len(tapes) != 2 {
		t.Fatalf("кассет в каталоге %d; want 2", len(tapes))
	}
}

// TestBackup_SpanningNonInteractiveWithoutFlag — неинтерактивный
// режим без --next-tape: понятная ошибка с подсказкой флага.
func TestBackup_SpanningNonInteractiveWithoutFlag(t *testing.T) {
	e := spanEnv(t, false, "")
	_, err := outOf(t, e, "backup", "media")
	if err == nil || !strings.Contains(err.Error(), "--next-tape") {
		t.Fatalf("backup: %v; want подсказка про --next-tape", err)
	}
}
