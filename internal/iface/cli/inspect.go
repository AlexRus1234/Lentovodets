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

// `lentovodec tape inspect <device>` — инвентарь кассеты без конфига и
// каталога (DR-путь, SPEC §4.5). Устройство — позиционный аргумент:
// команда не читает TOML и не открывает SQLite.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"lentovodec/internal/adapter/sloglog"
	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/usecase/inspect"
)

// newTapeInspectCmd — `lentovodec tape inspect <device>`.
func newTapeInspectCmd(deps Deps) *cobra.Command {
	var asJSON, showLabel, showSessions, showFiles bool
	cmd := &cobra.Command{
		Use:   "inspect <device>",
		Short: "Инвентарь кассеты без конфига и БД: ярлык и индексы сессий",
		Long: `Читает ярлык и JSON-индексы всех сессий прямо с устройства,
без конфигурации и SQLite-каталога: содержимое ленты самодостаточно,
каталог — производное. tar-потоки не читаются: целостность данных
проверяет tape readtest. Инвентарь — физический проход ленты
(seek-таблицы нет): полная кассета занимает минуты, не мгновенный ответ.

По умолчанию печатаются ярлык и сводка сессий; --label, --sessions,
--files выбирают разделы (--files подразумевает --sessions). --json —
машинный вывод.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			tape, err := deps.OpenTape(args[0])
			if err != nil {
				return err
			}
			defer func() {
				if err := tape.Close(); err != nil {
					fmt.Fprintf(deps.Stderr, "закрытие ленты: %v\n", err)
				}
			}()
			// Конфиг не читается вовсе: логгер — stderr уровня info.
			uc := inspect.New(tape, deps.Codec, sloglog.New("info", deps.Stderr))
			ctx, stop := signal.NotifyContext(
				context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			rep, err := uc.Inspect(ctx)
			if err != nil {
				return err
			}
			if asJSON {
				return printInspectJSON(deps, rep, showLabel, showSessions, showFiles)
			}
			printInspectText(deps, rep, showLabel, showSessions, showFiles)
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&asJSON, "json", false, "машинный вывод JSON")
	f.BoolVar(&showLabel, "label", false, "печатать ярлык")
	f.BoolVar(&showSessions, "sessions", false, "печатать сводку сессий")
	f.BoolVar(&showFiles, "files", false, "печатать списки файлов сессий (подразумевает --sessions)")
	return cmd
}

// inspectJobJSON — wire-вид снапшота задания (docs/FORMAT.md §5).
type inspectJobJSON struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Mode        string   `json:"mode"`
	Paths       []string `json:"paths"`
	Exclude     []string `json:"exclude,omitempty"`
	SpanDepth   int32    `json:"span_depth,omitempty"`
}

// inspectSessionJSON — wire-вид сессии для --json.
type inspectSessionJSON struct {
	SessionNum    int32              `json:"session_num"`
	Type          domain.SessionType `json:"type"`
	JobRunID      string             `json:"job_run_id"`
	Timestamp     int64              `json:"timestamp"`
	JobName       string             `json:"job_name,omitempty"`
	Part          int32              `json:"part,omitempty"`
	Continues     string             `json:"continues,omitempty"`
	ToolVersion   string             `json:"tool_version,omitempty"`
	ConfigSource  string             `json:"config_source,omitempty"`
	ConfigMTime   int64              `json:"config_mtime,omitempty"`
	JobConfigHash string             `json:"job_config_hash,omitempty"`
	JobConfig     *inspectJobJSON    `json:"job_config,omitempty"`
	FileCount     int                `json:"files"`
	Bytes         int64              `json:"bytes"`
	Entries       []domain.FileMeta  `json:"entries,omitempty"`
}

// inspectJSON — wire-вид отчёта: выбранные разделы.
type inspectJSON struct {
	Label        *domain.TapeLabel     `json:"label,omitempty"`
	Sessions     *[]inspectSessionJSON `json:"sessions,omitempty"`
	NextTapeName string                `json:"next_tape,omitempty"`
}

// printInspectJSON выводит отчёт машинным видом; флаги разделов
// управляют наполнением, как и в текстовом выводе.
func printInspectJSON(deps Deps, rep inspect.Report, showLabel, showSessions, showFiles bool) error {
	showLabel, showSessions = inspectSections(showLabel, showSessions, showFiles)
	out := inspectJSON{NextTapeName: rep.NextTapeName}
	if showLabel {
		out.Label = &rep.Label
	}
	if showSessions {
		sessions := make([]inspectSessionJSON, 0, len(rep.Sessions))
		for _, s := range rep.Sessions {
			sj := inspectSessionJSON{
				SessionNum:    s.Header.SessionNum,
				Type:          s.Header.Type,
				JobRunID:      s.Header.JobRunID,
				Timestamp:     s.Header.Timestamp,
				JobName:       s.Header.JobName,
				Part:          s.Header.Part,
				Continues:     s.Header.Continues,
				ToolVersion:   s.Header.ToolVersion,
				ConfigSource:  s.Header.ConfigSource,
				ConfigMTime:   s.Header.ConfigMTime,
				JobConfigHash: s.Header.JobConfigHash,
				JobConfig:     inspectJobOf(s.Header.JobConfig),
				FileCount:     len(s.Files),
				Bytes:         s.Bytes,
			}
			if showFiles {
				sj.Entries = s.Files
			}
			sessions = append(sessions, sj)
		}
		out.Sessions = &sessions
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("inspect: json: %w", err)
	}
	fmt.Fprintln(deps.Stdout, string(b))
	return nil
}

// printInspectText выводит отчёт человеком читаемым видом.
func printInspectText(deps Deps, rep inspect.Report, showLabel, showSessions, showFiles bool) {
	showLabel, showSessions = inspectSections(showLabel, showSessions, showFiles)
	w := deps.Stdout
	if showLabel {
		fmt.Fprintf(w, "кассета         %s\n", rep.Label.Name)
		fmt.Fprintf(w, "uuid            %s\n", rep.Label.UUID)
		fmt.Fprintf(w, "формат          %s (версия %d)\n", rep.Label.Magic, rep.Label.FormatVersion)
		fmt.Fprintf(w, "отформатирована %s\n", rep.Label.FormattedAt)
	}
	if showSessions {
		if showLabel {
			fmt.Fprintln(w)
		}
		for _, s := range rep.Sessions {
			line := fmt.Sprintf("сессия %d  %s  job %s  %s",
				s.Header.SessionNum, s.Header.Type, s.Header.JobName,
				time.Unix(s.Header.Timestamp, 0).UTC().Format(time.RFC3339))
			if s.Header.Part > 1 {
				line += fmt.Sprintf("  часть %d", s.Header.Part)
			}
			line += fmt.Sprintf("  файлов %d  %s", len(s.Files), humanSize(s.Bytes))
			fmt.Fprintln(w, line)
			printSnapshotLines(w, s.Header)
			if !showFiles {
				continue
			}
			for _, fm := range s.Files {
				fmt.Fprintf(w, "  %s  %s\n", string(fm.State), inspectFileLine(fm))
			}
		}
	}
	if rep.NextTapeName != "" {
		fmt.Fprintf(deps.Stdout, "цепочка продолжается: следующая кассета %s\n", rep.NextTapeName)
	}
}

// inspectJobOf переводит доменный снапшот в wire-вид --json; nil → nil.
func inspectJobOf(job *domain.Job) *inspectJobJSON {
	if job == nil {
		return nil
	}
	return &inspectJobJSON{
		Name:        job.Name,
		Description: job.Description,
		Mode:        string(job.Mode),
		Paths:       job.Paths,
		Exclude:     job.Exclude,
		SpanDepth:   job.SpanDepth,
	}
}

// printSnapshotLines — снапшот конфигурации под строкой сессии
// (docs/FORMAT.md §5): хеш пишется каждой новой сессией, тело — только
// когда оно записано (первая сессия кассеты или изменение конфигурации;
// nil — «не изменилось, см. предыдущую сессию с тем же хешем»).
// Отсутствие хеша — лента записана до снапшота: конфигурация неизвестна.
func printSnapshotLines(w io.Writer, h port.SessionHeader) {
	if h.JobConfigHash == "" {
		return
	}
	cfg := "  конфиг   sha256 " + h.JobConfigHash
	if h.ToolVersion != "" {
		cfg += "  версия " + h.ToolVersion
	}
	if h.ConfigSource != "" {
		cfg += "  источник " + h.ConfigSource
		if h.ConfigMTime != 0 {
			cfg += " (" + time.Unix(h.ConfigMTime, 0).UTC().Format(time.RFC3339) + ")"
		}
	}
	fmt.Fprintln(w, cfg)
	if jc := h.JobConfig; jc != nil {
		job := fmt.Sprintf("  задание  %s  режим %s", jc.Name, jc.Mode)
		if jc.SpanDepth > 0 {
			job += fmt.Sprintf("  span_depth %d", jc.SpanDepth)
		}
		job += "\n           пути " + strings.Join(jc.Paths, ", ")
		if len(jc.Exclude) > 0 {
			job += "\n           exclude " + strings.Join(jc.Exclude, ", ")
		}
		if jc.Description != "" {
			job += "\n           " + jc.Description
		}
		fmt.Fprintln(w, job)
	}
}

// inspectFileLine — запись индекса одной строкой: каталоги без размера,
// симлинки с целью, tombstone'ы без тела.
func inspectFileLine(fm domain.FileMeta) string {
	switch {
	case fm.IsDir:
		return fm.Path + "/"
	case fm.IsDeleted():
		return fm.Path
	case fm.IsSymlink():
		return fmt.Sprintf("%s -> %s", fm.Path, fm.Linkname)
	case fm.IsHardlink():
		return fmt.Sprintf("%s => %s", fm.Path, fm.Linkname)
	default:
		return fmt.Sprintf("%s  %s", fm.Path, humanSize(fm.Size))
	}
}

// inspectSections разрешает умолчание: без явных флагов — ярлык и
// сводка сессий; --files подразумевает --sessions.
func inspectSections(showLabel, showSessions, showFiles bool) (bool, bool) {
	if !showLabel && !showSessions && !showFiles {
		showLabel, showSessions = true, true
	}
	if showFiles {
		showSessions = true
	}
	return showLabel, showSessions
}
