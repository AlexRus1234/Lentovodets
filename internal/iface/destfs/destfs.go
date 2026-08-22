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

// Package destfs — декоратор port.Filesystem для восстановления в
// отдельный каталог: пути записи (MkdirAll/Create/Remove) переносятся
// под корень dest, чтение и обход проходят без изменений. Нужен CLI
// `restore --dest` и REST `POST /api/restore/start?dest=`; сам use case
// restore про dest не знает (пишет по путям из индекса ленты).
//
// Индекс ленты — данные наполовину доверенные (битые биты, зловредная
// кассета), поэтому перенос строг: путь с ".." или абсолютными
// выходами за dest отклоняется, запись через symlink запрещена — иначе
// восстановление писало бы файлы за пределами dest.
package destfs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"lentovodec/internal/port"
)

// Wrap оборачивает inner так, что файлы с путями из индекса ленты
// (абсолютные unix-пути или пути с именем тома) пишутся под dest:
// "/etc/hosts" → dest/etc/hosts. Пустой dest возвращает inner как есть.
func Wrap(inner port.Filesystem, dest string) port.Filesystem {
	if dest == "" {
		return inner
	}
	return &relocFS{inner: inner, dest: filepath.Clean(dest), checked: make(map[string]bool)}
}

// relocFS — port.Filesystem с переносом путей записи под dest.
// Не потокобезопасен: одна горутина восстановления владеет своим
// экземпляром (CLI-команда или задача демона).
type relocFS struct {
	inner port.Filesystem
	dest  string
	// checked — каталоги, для которых уже известно, что они не symlink
	// (проверка один раз на каталог: symlink'ом каталог может стать
	// только нашим же Symlink — кэш тогда инвалидируется).
	checked map[string]bool
}

// Walk передаётся внутренней ФС без изменений (в restore не участвует).
func (f *relocFS) Walk(ctx context.Context, root string, fn func(string, port.Entry) error) error {
	return f.inner.Walk(ctx, root, fn)
}

// Open передаётся внутренней ФС без изменений.
func (f *relocFS) Open(path string) (io.ReadCloser, error) {
	return f.inner.Open(path)
}

// Stat передаётся внутренней ФС без изменений.
func (f *relocFS) Stat(path string) (port.Entry, error) {
	return f.inner.Stat(path)
}

func (f *relocFS) Readlink(path string) (string, error) { return f.inner.Readlink(path) }

// ReadDir передаётся внутренней ФС без изменений: браузер показывает ФС
// сервера, а не виртуальное дерево каталога назначения.
func (f *relocFS) ReadDir(path string) ([]port.DirEntry, error) {
	return f.inner.ReadDir(path)
}

// MkdirAll создаёт каталог под dest. Сам путь и предки проверяются на
// symlink: MkdirAll молча «успешен» на symlink-на-каталог, а следующая
// за ним Create писала бы по ссылке.
func (f *relocFS) MkdirAll(path string, perm os.FileMode) error {
	joined, err := f.relocate(path)
	if err != nil {
		return err
	}
	if err := f.guardAncestors(joined); err != nil {
		return err
	}
	if joined != f.dest {
		if err := f.guardNotSymlink(joined); err != nil {
			return err
		}
	}
	return f.inner.MkdirAll(joined, perm)
}

// Create создаёт файл под dest: os.Create следует по symlink финального
// пути и предков — обе проверяются.
func (f *relocFS) Create(path string) (io.WriteCloser, error) {
	joined, err := f.relocate(path)
	if err != nil {
		return nil, err
	}
	if err := f.guardAncestors(joined); err != nil {
		return nil, err
	}
	if err := f.guardNotSymlink(joined); err != nil {
		return nil, err
	}
	return f.inner.Create(joined)
}

// Remove удаляет файл/каталог под dest (tombstone'ы mirror-restore).
// os.Remove следует по symlink-предкам (удаляет по ссылке), сам
// финальный symlink удаляется как ссылка — проверяются только предки.
func (f *relocFS) Remove(path string) error {
	joined, err := f.relocate(path)
	if err != nil {
		return err
	}
	if err := f.guardAncestors(joined); err != nil {
		return err
	}
	return f.inner.Remove(joined)
}

// Symlink создаёт symlink под dest: linkname — содержимое ссылки,
// переносится только сам путь. Предки проверяются (создание ссылки
// внутри symlink-каталога писало бы по ссылке); после создания кэш
// проверенных каталогов для пути инвалидируется — путь теперь ссылка.
func (f *relocFS) Symlink(linkname, path string) error {
	joined, err := f.relocate(path)
	if err != nil {
		return err
	}
	if err := f.guardAncestors(joined); err != nil {
		return err
	}
	if err := f.inner.Symlink(linkname, joined); err != nil {
		return err
	}
	f.evict(joined)
	return nil
}

// Link создаёт жёсткую ссылку под dest: проверяются предки обоих путей
// (os.Link следует по промежуточным каталогам).
func (f *relocFS) Link(oldname, newname string) error {
	oldJoined, err := f.relocate(oldname)
	if err != nil {
		return err
	}
	newJoined, err := f.relocate(newname)
	if err != nil {
		return err
	}
	if err := f.guardAncestors(oldJoined); err != nil {
		return err
	}
	if err := f.guardAncestors(newJoined); err != nil {
		return err
	}
	return f.inner.Link(oldJoined, newJoined)
}

// relocate переносит путь из индекса под корень dest: отрезаются
// ведущий '/' и имя тома ("C:"), остальное присоединяется к dest.
// Путь, вырывающийся за dest (".." в отрезанной части), отклоняется —
// индекс ленты не обязан быть честным.
func (f *relocFS) relocate(p string) (string, error) {
	clean := filepath.Clean(p)
	rest := strings.TrimPrefix(clean, filepath.VolumeName(clean))
	rest = filepath.ToSlash(rest)
	// Имя тома срезается и там, где текущая ОС его не видит: индекс
	// мог быть записан на другой ОС ("C:/data" при restore на Linux).
	if len(rest) > 2 && rest[0] != '/' && rest[1] == ':' && rest[2] == '/' {
		rest = rest[2:]
	}
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" || rest == "." {
		return f.dest, nil
	}
	if rest == ".." || strings.HasPrefix(rest, "../") {
		return "", fmt.Errorf("destfs: путь %q выходит за каталог назначения", p)
	}
	joined := filepath.Join(f.dest, filepath.FromSlash(rest))
	if rel, err := filepath.Rel(f.dest, joined); err != nil ||
		rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("destfs: путь %q выходит за каталог назначения", p)
	}
	return joined, nil
}

// guardAncestors проверяет, что ни один каталог-предок joined не
// symlink: запись (os.Create/MkdirAll/Remove/Link) следует по
// промежуточным ссылкам и ушла бы за dest. Проверенные каталоги
// кэшируются; предок проверенного каталога тоже проверен — обход
// снизу вверх останавливается на первом кэшированном.
func (f *relocFS) guardAncestors(joined string) error {
	var pending []string
	for dir := filepath.Dir(joined); dir != f.dest; dir = filepath.Dir(dir) {
		if f.checked[dir] {
			break
		}
		pending = append(pending, dir)
		if dir == "." || dir == string(filepath.Separator) || dir == "" {
			break // выше dest по cleanliness relocate не бывает — защита от цикла
		}
	}
	// проверка сверху вниз: кэшируются только каталоги с проверенными родителями
	for i := len(pending) - 1; i >= 0; i-- {
		if err := f.guardNotSymlink(pending[i]); err != nil {
			return err
		}
		f.checked[pending[i]] = true
	}
	return nil
}

// guardNotSymlink отклоняет путь, если он symlink.
func (f *relocFS) guardNotSymlink(p string) error {
	if _, err := f.inner.Readlink(p); err == nil {
		return fmt.Errorf(
			"destfs: запись через symlink %q запрещена (восстановление в каталог назначения)", p)
	}
	return nil
}

// evict выбрасывает path и его потомков из кэша проверенных каталогов:
// Symlink только что превратил путь (возможно, бывший каталог) в ссылку.
func (f *relocFS) evict(joined string) {
	prefix := joined + string(filepath.Separator)
	for k := range f.checked {
		if k == joined || strings.HasPrefix(k, prefix) {
			delete(f.checked, k)
		}
	}
}
