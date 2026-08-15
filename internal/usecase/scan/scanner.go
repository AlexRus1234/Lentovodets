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
	fs     port.Filesystem
	hasher port.Hasher
	prog   port.ProgressReporter // nil допустим
	log    *slog.Logger
}

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
		result []domain.FileMeta
		seen   = make(map[string]bool)
		bytes  int64
	)
	for _, root := range job.Paths {
		if err := s.fs.Walk(ctx, root, func(path string, info port.Entry) error {
			p := domain.NormalizePath(path)
			if excludedPath(p, job.Exclude) {
				return nil
			}
			if seen[p] {
				return nil
			}
			seen[p] = true

			cur := domain.FileMeta{
				Path:    p,
				Size:    info.Size(),
				ModTime: info.ModTime().UnixNano(),
				IsDir:   info.IsDir(),
			}
			prev, existed := snapshot[p]
			switch {
			case !existed:
				cur.State = domain.StateAdded
			case prev.Size == cur.Size && prev.ModTime == cur.ModTime:
				return nil // не изменился — в сессию не попадает
			default:
				cur.State = domain.StateModified
			}
			if !cur.IsDir {
				hash, err := s.hashFile(cur.Path)
				if err != nil {
					return err
				}
				cur.Hash = hash
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

	if job.Mode == domain.ModeMirror {
		result = append(result, tombstones(snapshot, seen, job)...)
	}
	s.log.Debug("scan finished", slog.Int("changed", len(result)))
	return result, nil
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
