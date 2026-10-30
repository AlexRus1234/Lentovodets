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

package tapeformat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"lentovodec/internal/domain"
	"lentovodec/internal/testutil"
)

// fixtureIndex — индекс для тестов КС: все покрытые поля непустые и
// различимые, чтобы мутация любого из них меняла каноническую форму.
func fixtureIndex() SessionIndex {
	return SessionIndex{
		FormatVersion: domain.FormatVersion,
		SessionNum:    2,
		Type:          domain.SessionInc,
		JobRunID:      "0f4c2d6a-1111-4222-8333-444455556666",
		Timestamp:     1700000000,
		JobName:       "media",
		Part:          1,
		Continues:     "uuid-prev",
		Files: []domain.FileMeta{{
			Path:     "/data/a.mkv",
			Size:     42,
			ModTime:  1700000001,
			Hash:     "0123456789abcdef",
			State:    domain.StateModified,
			Type:     domain.FileTypeSymlink,
			Linkname: "/data/b.mkv",
		}},
	}
}

// TestIndexDigest_CoversFields — любое покрытое поле входит в
// каноническую форму, служебные index_sha256/hash_algo — нет.
func TestIndexDigest_CoversFields(t *testing.T) {
	base := fixtureIndex()
	want := indexDigest(&base)

	mutants := map[string]func(*SessionIndex){
		"format_version": func(i *SessionIndex) { i.FormatVersion-- },
		"session_num":    func(i *SessionIndex) { i.SessionNum++ },
		"session_type":   func(i *SessionIndex) { i.Type = domain.SessionFull },
		"job_run_id":     func(i *SessionIndex) { i.JobRunID += "x" },
		"timestamp":      func(i *SessionIndex) { i.Timestamp++ },
		"job_name":       func(i *SessionIndex) { i.JobName += "x" },
		"part":           func(i *SessionIndex) { i.Part++ },
		"continues":      func(i *SessionIndex) { i.Continues += "x" },
		"files_count": func(i *SessionIndex) {
			i.Files = append(i.Files, i.Files[0])
		},
		"path":      func(i *SessionIndex) { i.Files[0].Path += "x" },
		"size":      func(i *SessionIndex) { i.Files[0].Size++ },
		"mod_time":  func(i *SessionIndex) { i.Files[0].ModTime++ },
		"is_dir":    func(i *SessionIndex) { i.Files[0].IsDir = true },
		"hash":      func(i *SessionIndex) { i.Files[0].Hash += "0" },
		"state":     func(i *SessionIndex) { i.Files[0].State = domain.StateAdded },
		"file_type": func(i *SessionIndex) { i.Files[0].Type = domain.FileTypeHardlink },
		"linkname":  func(i *SessionIndex) { i.Files[0].Linkname += "x" },
	}
	for name, mutate := range mutants {
		idx := base
		idx.Files = append([]domain.FileMeta(nil), base.Files...)
		mutate(&idx)
		if got := indexDigest(&idx); got == want {
			t.Errorf("%s: каноническая форма не изменилась", name)
		}
	}

	sealed := base
	sealed.Files = append([]domain.FileMeta(nil), base.Files...)
	sealIndex(&sealed)
	if sealed.HashAlgo != IndexHashAlgo {
		t.Errorf("HashAlgo = %q, want %q", sealed.HashAlgo, IndexHashAlgo)
	}
	if sealed.IndexSHA256 != want {
		t.Errorf("IndexSHA256 = %q, want %q", sealed.IndexSHA256, want)
	}
	if got := indexDigest(&sealed); got != want {
		t.Error("index_sha256/hash_algo попали в собственную каноническую форму")
	}
}

// TestVerifyIndexHash — отсутствие поля и незнакомое правило не
// блокируют чтение; подмена покрытого поля даёт IndexCorruptError.
func TestVerifyIndexHash(t *testing.T) {
	legacy := fixtureIndex()
	if err := verifyIndexHash(&legacy); err != nil {
		t.Errorf("лента без поля: %v, want nil", err)
	}

	sealed := fixtureIndex()
	sealIndex(&sealed)
	if err := verifyIndexHash(&sealed); err != nil {
		t.Errorf("целый индекс: %v, want nil", err)
	}

	unknown := sealed
	unknown.HashAlgo = "sha256-canon9"
	if err := verifyIndexHash(&unknown); err != nil {
		t.Errorf("незнакомое правило: %v, want nil (чтение не блокируется)", err)
	}

	tampered := sealed
	tampered.JobName = "other"
	var corrupt *domain.IndexCorruptError
	err := verifyIndexHash(&tampered)
	if !errors.As(err, &corrupt) {
		t.Fatalf("err = %v, want *domain.IndexCorruptError", err)
	}
	if corrupt.SessionNum != sealed.SessionNum || corrupt.Algo != IndexHashAlgo {
		t.Errorf("поля ошибки = %+v", corrupt)
	}
	if corrupt.Recorded != sealed.IndexSHA256 || corrupt.Computed == "" {
		t.Errorf("суммы в ошибке = %+v", corrupt)
	}
}

// TestWriteSession_SealsIndex — писатель проставляет поля, читатель
// проверяет их без замечаний.
func TestWriteSession_SealsIndex(t *testing.T) {
	ctx := context.Background()
	tape := testutil.NewFakeTape()
	idx := SessionIndex{
		FormatVersion: domain.FormatVersion,
		SessionNum:    1,
		Type:          domain.SessionFull,
		JobRunID:      "run",
		Timestamp:     1700000000,
		JobName:       "j",
	}
	if err := WriteSession(ctx, tape, idx, testutil.NewMapFS(nil), nil); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	blocks, _ := tape.Snapshot()
	var got SessionIndex
	if err := json.Unmarshal(trimZeros(blocks[0]), &got); err != nil {
		t.Fatalf("разбор записанного индекса: %v", err)
	}
	if got.HashAlgo != IndexHashAlgo || got.IndexSHA256 == "" {
		t.Fatalf("поля КС не записаны: algo=%q sum=%q", got.HashAlgo, got.IndexSHA256)
	}
	if err := verifyIndexHash(&got); err != nil {
		t.Errorf("verifyIndexHash записанного индекса: %v", err)
	}
}

// TestReadHeader_IndexCorruptIsDamage — порча индекса доходит до
// вызывающего и как IndexCorruptError (диагноз), и как SessionDamageError
// (лечится пропуском сессии, а не остановкой восстановления).
func TestReadHeader_IndexCorruptIsDamage(t *testing.T) {
	ctx := context.Background()
	idx := fixtureIndex()
	sealIndex(&idx)
	idx.JobName = "tampered-after-seal" // подмена после подписи

	padded, err := encodeBlocks(idx, 0)
	if err != nil {
		t.Fatalf("encodeBlocks: %v", err)
	}
	tape := testutil.NewFakeTape()
	for off := 0; off < len(padded); off += domain.BlockSize {
		if err := tape.WriteBlock(ctx, padded[off:off+domain.BlockSize]); err != nil {
			t.Fatalf("WriteBlock: %v", err)
		}
	}
	if err := tape.WriteEOF(ctx); err != nil {
		t.Fatalf("WriteEOF: %v", err)
	}
	if err := tape.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	_, err = ReadHeader(ctx, tape)
	var corrupt *domain.IndexCorruptError
	if !errors.As(err, &corrupt) {
		t.Fatalf("err = %v, want *domain.IndexCorruptError", err)
	}
	var dmg *domain.SessionDamageError
	if !errors.As(err, &dmg) {
		t.Fatalf("err = %v, want маркер SessionDamageError", err)
	}
}
