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

// Кодирование и декодирование ярлыка кассеты (docs/FORMAT.md §5).

package tapeformat

import (
	"encoding/json"
	"fmt"
	"strings"

	"lentovodec/internal/domain"
)

// magicFamily — префикс магических строк семейства форматов lentovodec.
// Ярлык с чужим префиксом — ErrForeignFormat (docs/FORMAT.md §11).
const magicFamily = "LENTOVODEC_TAPE_"

// EncodeLabel кодирует ярлык в один блок размером domain.BlockSize:
// JSON, добитый нулями. Возвращает ошибку, если JSON не влезает в блок.
func EncodeLabel(label domain.TapeLabel) ([]byte, error) {
	return encodeBlocks(label, 1)
}

// DecodeLabel разбирает блок ярлыка (нулевый padding игнорируется).
//
// Ошибки:
//   - все нули / пустой блок — *domain.BlankTapeError;
//   - не-JSON или чужой magic — *domain.ForeignFormatError;
//   - magic нашего семейства, но не текущий, либо format_version новее
//     поддерживаемой — *domain.NewerFormatError;
//   - некорректный formatted_at (не RFC-3339) — ошибка разбора: ярлык
//     нашего формата битый, читателю (tape info, restore, rebuild)
//     нужна честная ошибка, а не молчащее значение.
func DecodeLabel(block []byte) (domain.TapeLabel, error) {
	trimmed := trimZeros(block)
	if len(trimmed) == 0 {
		return domain.TapeLabel{}, &domain.BlankTapeError{}
	}
	var label domain.TapeLabel
	if err := json.Unmarshal(trimmed, &label); err != nil {
		return domain.TapeLabel{}, &domain.ForeignFormatError{Magic: snippet(trimmed)}
	}
	if label.Magic != domain.Magic {
		if !strings.HasPrefix(label.Magic, magicFamily) {
			return domain.TapeLabel{}, &domain.ForeignFormatError{Magic: label.Magic}
		}
		return domain.TapeLabel{}, &domain.NewerFormatError{
			Found:     label.FormatVersion,
			Supported: domain.FormatVersion,
		}
	}
	if label.FormatVersion > domain.FormatVersion {
		return domain.TapeLabel{}, &domain.NewerFormatError{
			Found:     label.FormatVersion,
			Supported: domain.FormatVersion,
		}
	}
	if _, err := label.ParseFormattedAt(); err != nil {
		return domain.TapeLabel{}, fmt.Errorf("tapeformat: %w", err)
	}
	return label, nil
}

// encodeBlocks кодирует v в JSON и добивает нулями до кратного
// domain.BlockSize; maxBlocks > 0 ограничивает число блоков.
func encodeBlocks(v any, maxBlocks int) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("tapeformat: JSON: %w", err)
	}
	if maxBlocks > 0 && len(data) > maxBlocks*domain.BlockSize {
		return nil, fmt.Errorf(
			"tapeformat: JSON (%d байт) не влезает в %d блок(ов) по %d байт",
			len(data), maxBlocks, domain.BlockSize)
	}
	return padToBlocks(data), nil
}

// padToBlocks добивает данные нулями до кратного domain.BlockSize.
// Предусловие: len(data) > 0.
func padToBlocks(data []byte) []byte {
	nblocks := (len(data) + domain.BlockSize - 1) / domain.BlockSize
	padded := make([]byte, nblocks*domain.BlockSize)
	copy(padded, data)
	return padded
}

// trimZeros отрезает замыкающие нулевые байты padding'а.
func trimZeros(b []byte) []byte {
	for len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return b
}

// snippet возвращает первые печатаемые байты блока для диагностики
// чужого формата.
func snippet(b []byte) string {
	const limit = 32
	var sb strings.Builder
	for i, r := range string(b) {
		if i >= limit {
			break
		}
		if r >= 0x20 && r < 0x7f {
			sb.WriteRune(r)
			continue
		}
		sb.WriteByte('.')
	}
	return sb.String()
}
