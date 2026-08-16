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

// Тестовые двойники web-пакета: конфиг, шагающие часы, сборка сервера.

package web_test

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	xxhashadapter "lentovodec/internal/adapter/xxhash"
	"lentovodec/internal/domain"
	"lentovodec/internal/iface/web"
	"lentovodec/internal/port"
	"lentovodec/internal/testutil"
)

// fakeConfig — web.ServerConfig в памяти.
type fakeConfig struct {
	mu        sync.Mutex
	jobs      []domain.Job
	device    string
	bind      string
	username  string
	passHash  string
	apiKey    string
	ttl       time.Duration
	tomlText  string
	serverURL string
	capacity  int64
	minTail   int64
}

func newFakeConfig() *fakeConfig {
	return &fakeConfig{
		device:    "/dev/nst0",
		bind:      "127.0.0.1:29201",
		ttl:       72 * time.Hour,
		tomlText:  "device = \"/dev/nst0\"\n",
		serverURL: "http://127.0.0.1:29201",
	}
}

func (c *fakeConfig) Jobs() ([]domain.Job, error) { return c.jobs, nil }
func (c *fakeConfig) Device() string              { return c.device }
func (c *fakeConfig) DB() string                  { return ":memory:" }
func (c *fakeConfig) Log() string                 { return "test.log" }
func (c *fakeConfig) Server() string              { return c.serverURL }
func (c *fakeConfig) LogLevel() string            { return "info" }
func (c *fakeConfig) Bind() string                { return c.bind }
func (c *fakeConfig) WebUsername() string         { return c.username }
func (c *fakeConfig) WebPasswordHash() string     { return c.passHash }
func (c *fakeConfig) APIKey() string              { return c.apiKey }
func (c *fakeConfig) SessionTTL() time.Duration   { return c.ttl }
func (c *fakeConfig) RawTOML() (string, error)    { return c.tomlText, nil }
func (c *fakeConfig) Capacity() (int64, error)    { return c.capacity, nil }
func (c *fakeConfig) MinTail() (int64, error)     { return c.minTail, nil }

func (c *fakeConfig) AddJob(job domain.Job) error {
	if err := job.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, j := range c.jobs {
		if j.Name == job.Name {
			return errDuplicateJob
		}
	}
	c.jobs = append(c.jobs, job)
	return nil
}

func (c *fakeConfig) RemoveJob(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.jobs[:0]
	found := false
	for _, j := range c.jobs {
		if j.Name == name {
			found = true
			continue
		}
		kept = append(kept, j)
	}
	if !found {
		return errMissingJob
	}
	c.jobs = kept
	return nil
}

// stepClock — порт.Clock с ручным продвижением времени.
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func newStepClock(t time.Time) *stepClock { return &stepClock{t: t} }

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *stepClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// idGen — последовательные идентификаторы задач.
type idGen struct {
	mu sync.Mutex
	n  int
}

func (g *idGen) next() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	return "task-" + string(rune('a'+g.n-1))
}

// testEnv — собранный сервер с двойниками.
type testEnv struct {
	srv    *httptest.Server
	tape   *testutil.FakeTape
	cat    *testutil.MemCatalog
	fs     *testutil.MapFS
	cfg    *fakeConfig
	clock  *stepClock
	codec  *testutil.FakeCodec
	server *web.Server
}

// newEnv собирает демона на двойниках; mutate настраивает Deps до сборки.
func newEnv(t *testing.T, mutate func(deps *web.Deps, env *testEnv)) *testEnv {
	t.Helper()

	env := &testEnv{
		tape:  testutil.NewFakeTape(),
		cat:   testutil.NewMemCatalog(),
		fs:    testutil.NewMapFS(nil),
		cfg:   newFakeConfig(),
		clock: newStepClock(time.Unix(1700000000, 0)),
		codec: &testutil.FakeCodec{},
	}
	deps := &web.Deps{
		Log:     testutil.NoopLogger(),
		Version: "test",
		Config:  env.cfg,
		Editor:  env.cfg,
		Catalog: env.cat,
		FS:      env.fs,
		Codec:   env.codec,
		Hasher:  xxhashadapter.New(),
		Rand:    testutil.FixedRand("11111111-1111-4111-8111-111111111111"),
		Clock:   env.clock,
		OpenTape: func(string) (port.Tape, error) {
			return env.tape, nil
		},
		NewTaskID: (&idGen{}).next,
	}
	if mutate != nil {
		mutate(deps, env)
	}
	srv, err := web.NewServer(*deps)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	env.server = srv
	env.srv = httptest.NewServer(srv.Handler())
	t.Cleanup(env.srv.Close)
	return env
}

// errDuplicateJob / errMissingJob — ошибки редактора заданий.
type strErr string

func (e strErr) Error() string { return string(e) }

const (
	errDuplicateJob = strErr("задание уже существует")
	errMissingJob   = strErr("задание не найдено")
)
