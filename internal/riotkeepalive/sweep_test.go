package riotkeepalive

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedAccount(t *testing.T, root, name string, withSession bool) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if !withSession {
		// An account saved with no persisted login: other files, no settings yaml.
		if err := os.WriteFile(filepath.Join(dir, "RiotClientSettings.yaml"), []byte("x: 1\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return ""
	}
	p := filepath.Join(dir, SettingsFileName)
	if err := os.WriteFile(p, fixture(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCollectTargets(t *testing.T) {
	root := t.TempDir()
	seedAccount(t, root, "main", true)
	seedAccount(t, root, "smurf", true)
	seedAccount(t, root, "nologin", false)
	if err := os.WriteFile(filepath.Join(root, "ids.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := CollectTargets(root)
	if err != nil {
		t.Fatalf("CollectTargets: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 targets, got %d: %+v", len(got), got)
	}
	names := map[string]bool{}
	for _, g := range got {
		names[g.AccountName] = true
	}
	if !names["main"] || !names["smurf"] || names["nologin"] {
		t.Fatalf("wrong targets: %+v", got)
	}
}

func TestCollectTargetsMissingRootIsNotAnError(t *testing.T) {
	got, err := CollectTargets(filepath.Join(t.TempDir(), "nope"))
	if err != nil || got != nil {
		t.Fatalf("want (nil, nil), got (%v, %v)", got, err)
	}
}

func newSweeper(t *testing.T, root string, h http.HandlerFunc, running func() bool) (*Sweeper, func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	c := NewClient()
	c.HTTP = srv.Client()
	c.Endpoint = srv.URL
	s := &Sweeper{
		CacheRoot:     root,
		Client:        c,
		ClientRunning: running,
		Spacing:       time.Nanosecond,
		now:           func() time.Time { return time.UnixMilli(1788700000000) },
		sleep:         func(context.Context, time.Duration) {},
	}
	return s, srv.Close
}

func TestSweepOnceRequiresGuard(t *testing.T) {
	s := &Sweeper{CacheRoot: t.TempDir()}
	if _, err := s.SweepOnce(context.Background()); !errors.Is(err, ErrNoGuard) {
		t.Fatalf("want ErrNoGuard, got %v", err)
	}
}

func TestSweepOnceSkipsWhileClientRunning(t *testing.T) {
	root := t.TempDir()
	seedAccount(t, root, "main", true)
	called := false
	s, done := newSweeper(t, root, func(w http.ResponseWriter, r *http.Request) {
		called = true
	}, func() bool { return true })
	defer done()

	if _, err := s.SweepOnce(context.Background()); !errors.Is(err, ErrClientRunning) {
		t.Fatalf("want ErrClientRunning, got %v", err)
	}
	if called {
		t.Fatal("token endpoint was contacted while the client was running")
	}
}

func TestSweepOnceRefreshesAndPersists(t *testing.T) {
	root := t.TempDir()
	p := seedAccount(t, root, "main", true)
	s, done := newSweeper(t, root, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT","id_token":"NEW_ID","refresh_token":"NEW_RT","expires_in":3600}`))
	}, func() bool { return false })
	defer done()

	results, err := s.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if len(results) != 1 || !results[0].Refreshed {
		t.Fatalf("unexpected results: %+v", results)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if sess.RefreshToken != "NEW_RT" || sess.IDToken != "NEW_ID" {
		t.Fatalf("token not persisted: %+v", sess)
	}
	if sess.WriteCount != 2 {
		t.Fatalf("write count not incremented: %d", sess.WriteCount)
	}
	if !strings.Contains(string(data), "\r\n") {
		t.Fatal("CRLF endings lost on write-back")
	}
}

func TestSweepOnceMarksExpiredAccounts(t *testing.T) {
	root := t.TempDir()
	p := seedAccount(t, root, "main", true)
	before, _ := os.ReadFile(p)

	s, done := newSweeper(t, root, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}, func() bool { return false })
	defer done()

	results, err := s.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if len(results) != 1 || !results[0].NeedsLogin || results[0].Refreshed {
		t.Fatalf("unexpected results: %+v", results)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("a rejected refresh must leave the saved file untouched")
	}
}

func TestSweepOnceHaltsIfClientStartsMidSweep(t *testing.T) {
	root := t.TempDir()
	seedAccount(t, root, "a", true)
	seedAccount(t, root, "b", true)
	seedAccount(t, root, "c", true)

	calls := 0
	running := false
	s, done := newSweeper(t, root, func(w http.ResponseWriter, r *http.Request) {
		calls++
		running = true // the user launched Riot right after the first refresh
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT","id_token":"I","refresh_token":"R","expires_in":3600}`))
	}, func() bool { return running })
	defer done()

	results, err := s.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if calls != 1 || len(results) != 1 {
		t.Fatalf("sweep did not halt: calls=%d results=%d", calls, len(results))
	}
}
