// FakeCodec — двойник port.TapeCodec: JSON-ярлык без паддинга,
// сессии только записываются в память. См. docs/TESTING.md §3.

package testutil

import (
	"context"
	"encoding/json"

	"lentovodec/internal/domain"
	"lentovodec/internal/port"
)

// FakeCodec запоминает вызовы; поля Err* инъектируют сбои, ReadFiles —
// результат ReadSession.
type FakeCodec struct {
	ErrEncode     error
	ErrDecode     error
	ErrWrite      error
	ErrRead       error
	ReadFiles     []domain.FileMeta
	WroteHeaders  []port.SessionHeader
	WroteFiles    [][]domain.FileMeta
	WroteSessions int
}

// EncodeLabel кодирует ярлык в JSON (без паддинга до BlockSize).
func (c *FakeCodec) EncodeLabel(label domain.TapeLabel) ([]byte, error) {
	if c.ErrEncode != nil {
		return nil, c.ErrEncode
	}
	return json.Marshal(label)
}

// DecodeLabel разбирает JSON-ярлык; пустой блок — BlankTapeError,
// чужой JSON/magic — ForeignFormatError, новая версия — NewerFormatError.
func (c *FakeCodec) DecodeLabel(block []byte) (domain.TapeLabel, error) {
	if c.ErrDecode != nil {
		return domain.TapeLabel{}, c.ErrDecode
	}
	trimmed := trimZeroBytes(block)
	if len(trimmed) == 0 {
		return domain.TapeLabel{}, &domain.BlankTapeError{}
	}
	var label domain.TapeLabel
	if err := json.Unmarshal(trimmed, &label); err != nil {
		return domain.TapeLabel{}, &domain.ForeignFormatError{Magic: "fake"}
	}
	if label.Magic != domain.Magic {
		return domain.TapeLabel{}, &domain.ForeignFormatError{Magic: label.Magic}
	}
	if label.FormatVersion > domain.FormatVersion {
		return domain.TapeLabel{}, &domain.NewerFormatError{
			Found: label.FormatVersion, Supported: domain.FormatVersion}
	}
	return label, nil
}

// WriteSession запоминает заголовок и файлы сессии.
func (c *FakeCodec) WriteSession(
	ctx context.Context,
	tape port.Tape,
	header port.SessionHeader,
	files []domain.FileMeta,
	fs port.FileReader,
	prog port.ProgressReporter,
) error {
	if c.ErrWrite != nil {
		return c.ErrWrite
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.WroteHeaders = append(c.WroteHeaders, header)
	c.WroteFiles = append(c.WroteFiles, append([]domain.FileMeta(nil), files...))
	c.WroteSessions++
	return nil
}

// ReadSession возвращает ReadFiles.
func (c *FakeCodec) ReadSession(
	ctx context.Context,
	tape port.Tape,
	dest port.FileWriter,
	prog port.ProgressReporter,
) ([]domain.FileMeta, error) {
	if c.ErrRead != nil {
		return nil, c.ErrRead
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]domain.FileMeta(nil), c.ReadFiles...), nil
}

// trimZeroBytes отрезает замыкающие нули.
func trimZeroBytes(b []byte) []byte {
	for len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return b
}
