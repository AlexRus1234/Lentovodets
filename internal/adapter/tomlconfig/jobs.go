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

// Изменение заданий с записью обратно в TOML (port.ConfigEditor).
// Viper переписывает файл целиком: комментарии не сохраняются —
// принятое упрощение подхода (docs/ROADMAP.md, Этап 4).

package tomlconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"lentovodec/internal/domain"
)

// AddJob добавляет задание в [[jobs]] и записывает файл.
// Невалидное задание или дубликат имени — ошибка, файл не меняется.
// Чтение-модификация-запись атомарны под мьютексом Config: параллельные
// AddJob не теряют обновления друг друга.
func (c *Config) AddJob(job domain.Job) error {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("tomlconfig: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	jobs, err := c.jobsLocked()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.Name == job.Name {
			return fmt.Errorf("tomlconfig: задание %q уже существует", job.Name)
		}
	}
	list := append(jobMaps(jobs), jobToMap(job))
	return c.setJobs(list)
}

// RemoveJob удаляет задание по имени; отсутствие имени — ошибка,
// файл не меняется.
func (c *Config) RemoveJob(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	jobs, err := c.jobsLocked()
	if err != nil {
		return err
	}
	kept := make([]map[string]any, 0, len(jobs))
	found := false
	for _, j := range jobs {
		if j.Name == name {
			found = true
			continue
		}
		kept = append(kept, jobToMap(j))
	}
	if !found {
		return fmt.Errorf("tomlconfig: задание %q не найдено", name)
	}
	return c.setJobs(kept)
}

// setJobs обновляет список заданий в файле (сначала запись, потом
// слоёное представление — при сбое записи память консистентна диску).
// Запись атомарна: новый файл собирается рядом во временном файле и
// переименовывается поверх старого — сбой на середине записи (диск
// переполнен, процесс убит) не оставляет обрезанный конфиг, оригинал
// либо целиком старый, либо целиком новый. Вызывается под мьютексом.
func (c *Config) setJobs(list []map[string]any) error {
	c.file.Set(keyJobs, list)
	// имя временного файла обязано кончаться на .toml: viper выводит
	// формат записи из расширения
	tmp, err := os.CreateTemp(filepath.Dir(c.path), filepath.Base(c.path)+".tmp-*.toml")
	if err != nil {
		return fmt.Errorf("tomlconfig: временный файл рядом с %s: %w", c.path, err)
	}
	tmpName := tmp.Name()
	// права исходного файла, если он есть; CreateTemp даёт 0600 — для
	// нового конфига это уместно (внутри bcrypt-хеш пароля)
	if info, statErr := os.Stat(c.path); statErr == nil {
		_ = tmp.Chmod(info.Mode().Perm()) // best-effort: платформы различаются
	}
	closeErr := tmp.Close()
	writeErr := c.file.WriteConfigAs(tmpName)
	if writeErr == nil {
		writeErr = os.Rename(tmpName, c.path)
	}
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmpName)
		if writeErr != nil {
			return fmt.Errorf("tomlconfig: запись %s: %w", c.path, writeErr)
		}
		return fmt.Errorf("tomlconfig: запись %s: %w", c.path, closeErr)
	}
	c.read.Set(keyJobs, list)
	return nil
}

// jobMaps приводит существующие задания к записываемому виду.
func jobMaps(jobs []domain.Job) []map[string]any {
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobToMap(j))
	}
	return out
}

// jobToMap — доменное задание как map для [[jobs]] в TOML.
// span_depth = 0 не пишется (дефолт; существующие конфиги не меняются).
func jobToMap(j domain.Job) map[string]any {
	m := map[string]any{
		"name":        j.Name,
		"description": j.Description,
		"mode":        string(j.Mode),
		"paths":       j.Paths,
		"exclude":     j.Exclude,
	}
	if j.SpanDepth != 0 {
		m["span_depth"] = j.SpanDepth
	}
	return m
}
