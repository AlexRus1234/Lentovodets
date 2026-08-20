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

// Тесты ReadIndexFiles: индекс сессии (заголовок + файлы) без чтения
// tar-потока — путь реконструкции каталога (rebuild).

package tapeformat_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"lentovodec/internal/adapter/tapeformat"
	"lentovodec/internal/domain"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

// readIdxHeaderOf — заголовок индекса сессии как он лежит на ленте;
// Part нормализуется декодером (отсутствующее поле — часть 1).
func idxHeaderOf(idx tapeformat.SessionIndex) port.SessionHeader {
	part := idx.Part
	if part < 1 {
		part = 1
	}
	return port.SessionHeader{
		SessionNum: idx.SessionNum, Type: idx.Type, JobRunID: idx.JobRunID,
		Timestamp: idx.Timestamp, JobName: idx.JobName,
		Part: part, Continues: idx.Continues,
	}
}

func TestReadIndexFiles_RoundTripThroughCodec(t *testing.T) {
	ctx := context.Background()
	fs, idx := buildFixture(t)
	tape := testutil.NewFakeTape()
	writeFixtureSession(t, tape, idx, fs)
	if err := tape.Rewind(ctx); err != nil {
		t.Fatal(err)
	}

	// через фасад кодека: порт-метод читает заголовок и файлы
	codec := tapeformat.NewCodec()
	header, files, err := codec.ReadIndexFiles(ctx, tape)
	if err != nil {
		t.Fatalf("ReadIndexFiles: %v", err)
	}
	if header != idxHeaderOf(idx) {
		t.Errorf("заголовок = %+v; want %+v", header, idxHeaderOf(idx))
	}
	if !reflect.DeepEqual(files, idx.Files) {
		t.Errorf("файлы = %+v; want %+v", files, idx.Files)
	}

	// позиция — за filemark'ом индекса, в начале tar-сегмента:
	// следующее чтение отдаёт данные tar, а не индекс следующей сессии
	block, err := tape.ReadBlock(ctx)
	if err != nil || len(block) == 0 {
		t.Fatalf("позиция после индекса: блок %d байт, err %v; want данные tar", len(block), err)
	}
}

func TestReadIndexFiles_SessionsSequential(t *testing.T) {
	ctx := context.Background()
	src := testutil.NewMapFS(map[string]string{"/s/a.txt": "alpha", "/s/b.txt": "beta"})
	_, s1 := singleFileFixture("/s/a.txt", "alpha")
	_, s2 := singleFileFixture("/s/b.txt", "beta")
	s1.SessionNum, s2.SessionNum = 1, 2

	tape := testutil.NewFakeTape()
	writeFixtureSession(t, tape, s1, src)
	if err := tape.EndOfData(ctx); err != nil {
		t.Fatal(err)
	}
	writeFixtureSession(t, tape, s2, src)
	if err := tape.Rewind(ctx); err != nil {
		t.Fatal(err)
	}

	// как rebuild: индекс → пропуск tar-сегмента FSF(1) → индекс → … → EOD
	for i, want := range []tapeformat.SessionIndex{s1, s2} {
		header, files, err := tapeformat.ReadIndexFiles(ctx, tape)
		if err != nil {
			t.Fatalf("сессия %d: ReadIndexFiles: %v", i+1, err)
		}
		if header != idxHeaderOf(want) {
			t.Errorf("сессия %d: заголовок = %+v; want %+v", i+1, header, idxHeaderOf(want))
		}
		if !reflect.DeepEqual(files, want.Files) {
			t.Errorf("сессия %d: файлы = %+v; want %+v", i+1, files, want.Files)
		}
		if err := tape.ForwardFilemarks(ctx, 1); err != nil {
			t.Fatalf("сессия %d: пропуск tar: %v", i+1, err)
		}
	}
	if _, _, err := tapeformat.ReadIndexFiles(ctx, tape); !errors.Is(err, &domain.EmptyIndexError{}) {
		t.Fatalf("после всех сессий: %v; want EmptyIndexError (EOD)", err)
	}
}

func TestReadIndexFiles_EmptyIndexIsEOD(t *testing.T) {
	tape := testutil.NewFakeTape()
	if _, _, err := tapeformat.ReadIndexFiles(context.Background(), tape); !errors.Is(err, &domain.EmptyIndexError{}) {
		t.Fatalf("пустая лента: %v; want EmptyIndexError", err)
	}
}

func TestReadIndexFiles_ContinuationBlock(t *testing.T) {
	ctx := context.Background()
	src := testutil.NewMapFS(map[string]string{"/s/a.txt": "alpha"})
	_, s1 := singleFileFixture("/s/a.txt", "alpha")
	s1.Part = 1
	tape := testutil.NewFakeTape()
	writeFixtureSession(t, tape, s1, src)
	// кассета с продолжением: за filemark'ом tar — блок-указатель,
	// затем закрывающая EOD-пара (FORMAT §6.1)
	if err := tapeformat.WriteContinuation(ctx, tape, port.Continuation{
		JobRunID: "run-1", SessionNum: 1, Part: 2, NextTapeName: "media-014",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tape.WriteEOF(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tape.WriteEOF(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tape.Rewind(ctx); err != nil {
		t.Fatal(err)
	}

	if _, _, err := tapeformat.ReadIndexFiles(ctx, tape); err != nil {
		t.Fatalf("сессия 1: %v", err)
	}
	if err := tape.ForwardFilemarks(ctx, 1); err != nil {
		t.Fatal(err)
	}
	_, _, err := tapeformat.ReadIndexFiles(ctx, tape)
	var cont *domain.ContinuationError
	if !errors.As(err, &cont) {
		t.Fatalf("после tar: %v; want ContinuationError", err)
	}
	if cont.JobRunID != "run-1" || cont.SessionNum != 1 || cont.Part != 2 || cont.NextTapeName != "media-014" {
		t.Fatalf("указатель: %+v", cont)
	}
}

func TestReadIndexFiles_ForeignBlock(t *testing.T) {
	tape := craftSessionTape(t, []byte("{ not json"), craftTar(t))
	if _, _, err := tapeformat.ReadIndexFiles(context.Background(), tape); err == nil {
		t.Fatal("чужой блок индекса = nil")
	}
}
