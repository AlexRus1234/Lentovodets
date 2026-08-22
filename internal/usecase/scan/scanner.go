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

// Сканер файловой системы: сравнение текущего дерева с прошлым снимком.

package scan

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// Scanner вычисляет []domain.FileMeta для ближайшей сессии бекапа:
// Added/Modified для изменившихся путей, Deleted-tombstone'ы для
// пропавших (только режим mirror). Неизменённые пути не возвращаются.
type Scanner struct {
	fs              port.Filesystem
	hasher          port.Hasher
	prog            port.ProgressReporter // nil допустим
	log             *slog.Logger
	skippedSpecials int
}

// SkippedSpecials returns the number of unsupported special entries skipped by
// the most recent Scan call.
func (s *Scanner) SkippedSpecials() int { return s.skippedSpecials }

// New создаёт сканер. Прошлый снимок передаётся в Scan, поэтому
// каталог сканеру не нужен.
func New(fs port.Filesystem, hasher port.Hasher, prog port.ProgressReporter, log *slog.Logger) *Scanner {
	return &Scanner{fs: fs, hasher: hasher, prog: prog, log: log}
}

// Scan обходит корни job.Paths, применяет job.Exclude (шаблон, сматчивший
// путь или любого его каталога-предка, отсекает всё поддерево) и сравнивает
// результат с lastSnapshot (файлы предыдущей сессии; путь → FileMeta).
// Порядок: обход корней в порядке job.Paths, tombstone'ы в конце по
// алфавиту.
func (s *Scanner) Scan(ctx context.Context, job domain.Job, lastSnapshot []domain.FileMeta) ([]domain.FileMeta, error) {
	if err := job.Validate(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	snapshot := make(map[string]domain.FileMeta, len(lastSnapshot))
	for _, fm := range lastSnapshot {
		snapshot[domain.NormalizePath(fm.Path)] = fm
	}

	s.log.Debug("scan started",
		slog.String("job", job.Name),
		slog.String("mode", string(job.Mode)),
		slog.Int("snapshot_files", len(snapshot)))

	var (
		result          []domain.FileMeta
		seen            = make(map[string]bool)
		links           = make(map[string]string)
		bytes           int64
		skippedSpecials int
	)
	s.skippedSpecials = 0
	for _, root := range job.Paths {
		if err := s.fs.Walk(ctx, root, func(path string, info port.Entry) error {
			cur, include, special, err := s.scanEntry(path, info, job, snapshot, seen, links)
			if err != nil {
				return err
			}
			if special {
				skippedSpecials++
				return nil
			}
			if !include {
				return nil
			}
			if !cur.IsDir && !cur.IsSymlink() && !cur.IsHardlink() {
				bytes += cur.Size
			}
			s.report(port.ProgressUpdate{
				Phase:          port.PhaseScan,
				CurrentFile:    cur.Path,
				ProcessedBytes: bytes,
			})
			result = append(result, cur)
			return nil
		}); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
	}
	// The use case obtains this count through the scan result metadata. Special
	// entries are intentionally not represented in the tape index.
	s.skippedSpecials = skippedSpecials

	if job.Mode == domain.ModeMirror {
		result = append(result, tombstones(snapshot, seen, job)...)
	}
	s.log.Debug("scan finished", slog.Int("changed", len(result)))
	return result, nil
}

func (s *Scanner) scanEntry(
	path string,
	info port.Entry,
	job domain.Job,
	snapshot map[string]domain.FileMeta,
	seen map[string]bool,
	links map[string]string,
) (domain.FileMeta, bool, bool, error) {
	p := domain.NormalizePath(path)
	if seen[p] {
		return domain.FileMeta{}, false, false, nil // перекрытие корней / исключён ранее
	}
	// seen отмечается и для исключённых путей: exclude — «не смотреть»,
	// а не «удалено». Иначе путь, исключённый между запусками mirror,
	// получал бы tombstone, и восстановление mirror удаляло бы живой файл.
	seen[p] = true
	if excludedPath(p, job.Exclude) {
		return domain.FileMeta{}, false, false, nil
	}
	if port.IsSpecial(info.Mode()) {
		s.log.Warn("special filesystem entry skipped", slog.String("path", p))
		return domain.FileMeta{}, false, true, nil
	}
	cur, err := s.entryMeta(path, p, info)
	if err != nil {
		return domain.FileMeta{}, false, false, err
	}
	// Идентичность inode регистрируется до проверки «не изменился»:
	// неизменённый владелец пары остаётся якорем для второго участника.
	setHardlink(&cur, info.LinkID(), links)
	prev, existed := snapshot[p]
	if existed && unchanged(cur, prev) {
		return domain.FileMeta{}, false, false, nil
	}
	if existed {
		cur.State = domain.StateModified
	} else {
		cur.State = domain.StateAdded
	}
	if !cur.IsDir && !cur.IsSymlink() && !cur.IsHardlink() {
		cur.Hash, err = s.hashFile(cur.Path)
		if err != nil {
			return domain.FileMeta{}, false, false, err
		}
	}
	return cur, true, false, nil
}

// unchanged сравнивает запись с прошлым снимком: совпадение размера,
// mtime, класса записи и цели ссылки означает отсутствие изменений.
// Тип сравнивается нормализованно: пустое значение и 'reg' эквивалентны
// (снимок из мигрированной БД хранит 'reg', свежий скан — пустой тип).
func unchanged(cur, prev domain.FileMeta) bool {
	return prev.Size == cur.Size && prev.ModTime == cur.ModTime &&
		prev.Type.Normalized() == cur.Type.Normalized() &&
		prev.Linkname == cur.Linkname
}

func (s *Scanner) entryMeta(path, normalized string, info port.Entry) (domain.FileMeta, error) {
	cur := domain.FileMeta{Path: normalized, ModTime: info.ModTime().UnixNano(), IsDir: info.IsDir()}
	if port.IsSymlink(info.Mode()) {
		link, err := s.fs.Readlink(path)
		if err != nil {
			return domain.FileMeta{}, fmt.Errorf("scan: readlink %q: %w", path, err)
		}
		cur.Type, cur.Linkname = domain.FileTypeSymlink, link
		cur.Size = int64(len(link))
	} else if !cur.IsDir {
		cur.Size = info.Size()
	}
	return cur, nil
}

func setHardlink(cur *domain.FileMeta, id string, links map[string]string) {
	if cur.IsDir || cur.IsSymlink() || id == "" {
		return
	}
	if first, ok := links[id]; ok {
		cur.Type, cur.Linkname = domain.FileTypeHardlink, first
		return
	}
	links[id] = cur.Path
}

// tombstones возвращает Deleted-записи для путей снимка, отсутствующих
// в текущем обходе и лежащих под корнями задания; по алфавиту.
func tombstones(snapshot map[string]domain.FileMeta, seen map[string]bool, job domain.Job) []domain.FileMeta {
	var gone []domain.FileMeta
	for p, prev := range snapshot {
		if seen[p] || !underRoots(p, job.Paths) {
			continue
		}
		gone = append(gone, domain.FileMeta{
			Path:  p,
			IsDir: prev.IsDir,
			State: domain.StateDeleted,
		})
	}
	sort.Slice(gone, func(i, j int) bool { return gone[i].Path < gone[j].Path })
	return gone
}

// underRoots сообщает, что путь — сам root или лежит под одним из roots.
func underRoots(p string, roots []string) bool {
	for _, root := range roots {
		r := domain.NormalizePath(root)
		if r == "/" || p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

// excludedPath сообщает, что путь исключён шаблонами: напрямую или через
// каталога-предка (предок внутри дерева обхода — всегда каталог, поэтому
// матч предка отсекает всё поддерево независимо от порядка Walk).
func excludedPath(p string, patterns []string) bool {
	if domain.MatchExclude(p, patterns) {
		return true
	}
	for i := strings.LastIndex(p, "/"); i > 0; i = strings.LastIndex(p[:i], "/") {
		if domain.MatchExclude(p[:i], patterns) {
			return true
		}
	}
	return false
}

// hashFile открывает файл и возвращает его контрольную сумму.
func (s *Scanner) hashFile(path string) (string, error) {
	src, err := s.fs.Open(path)
	if err != nil {
		return "", fmt.Errorf("scan: открытие %q: %w", path, err)
	}
	hash, hashErr := s.hasher.Hash(src)
	closeErr := src.Close()
	if hashErr != nil {
		return "", fmt.Errorf("scan: хеш %q: %w", path, hashErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("scan: закрытие %q: %w", path, closeErr)
	}
	return hash, nil
}

// report публикует снимок прогресса, если репортёр задан.
func (s *Scanner) report(u port.ProgressUpdate) {
	if s.prog != nil {
		s.prog.Update(u)
	}
}
