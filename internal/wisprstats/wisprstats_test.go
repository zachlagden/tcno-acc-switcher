package wisprstats

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sessionFile(t *testing.T, access, refresh string, expiresAt int64, userID string) []byte {
	t.Helper()
	inner, err := json.Marshal(map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "bearer",
		"expires_in":    604800,
		"expires_at":    expiresAt,
		"user":          map[string]any{"id": userID, "email": userID + "@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	outer, err := json.MarshalIndent(map[string]string{SessionKey: string(inner)}, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	return outer
}

func configFile(words int64, wpm float64, last string) []byte {
	return []byte(`{"prefs":{"cache":{"statistics":{"weekStreak":4,"dayStreak":3,"totalWords":` +
		itoa(words) + `,"totalDuration":1000,"totalNonEmptyDuration":900,"averageWPM":` +
		ftoa(wpm) + `,"wordsThisWeek":120,"wordsToday":10,"lastTranscriptTimestamp":"` + last +
		`","totalApps":["Discord","Code"]}}}}`)
}

func itoa(n int64) string   { b, _ := json.Marshal(n); return string(b) }
func ftoa(f float64) string { b, _ := json.Marshal(f); return string(b) }

const liveStatsBody = `{"is_initialized":true,"total_words":5000,"total_duration":2200,"total_non_empty_duration":2000,
"words_per_minute":150,"day_streak":6,"week_streak":9,"words_this_week":700,"last_transcript_timestamp":"2026-09-10T12:10:49.413221",
"total_apps":["a","b","c"],"desktop_total_words":4000,"desktop_words_per_minute":155,"desktop_words_this_week":500,
"desktop_total_non_empty_duration":1500,"desktop_last_transcript_timestamp":"2026-09-09T10:00:00.000000",
"mobile_total_words":1000,"mobile_words_per_minute":130,"mobile_words_this_week":200,"mobile_total_non_empty_duration":500,
"mobile_last_transcript_timestamp":"2026-09-10T12:10:49.413221"}`

var fixedNow = time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)

func TestParseSession_readsNestedValue(t *testing.T) {
	t.Parallel()
	s, err := ParseSession(sessionFile(t, "acc", "ref", 123, "u1"))
	if err != nil {
		t.Fatalf("ParseSession: %v", err)
	}
	if s.AccessToken != "acc" || s.RefreshToken != "ref" || s.ExpiresAt != 123 || s.UserID != "u1" || s.Email != "u1@example.com" {
		t.Errorf("got %+v", s)
	}
}

func TestParseSession_missingKey(t *testing.T) {
	t.Parallel()
	if _, err := ParseSession([]byte(`{"other":"x"}`)); err != ErrNoSession {
		t.Errorf("err = %v, want ErrNoSession", err)
	}
}

func TestSessionValidAt(t *testing.T) {
	t.Parallel()
	s := &Session{AccessToken: "a", ExpiresAt: 1000}
	if !s.ValidAt(900, 60) {
		t.Error("token with 100s left should be valid with a 60s margin")
	}
	if s.ValidAt(950, 60) {
		t.Error("token with 50s left should not be valid with a 60s margin")
	}
}

func TestApplyRefresh_rotatesTokensAndKeepsOtherFields(t *testing.T) {
	t.Parallel()
	before := sessionFile(t, "old-acc", "old-ref", 100, "u1")
	tok := &TokenResponse{AccessToken: "new-acc", RefreshToken: "new-ref", ExpiresIn: 604800}

	out, err := ApplyRefresh(before, tok, 1_000)
	if err != nil {
		t.Fatalf("ApplyRefresh: %v", err)
	}
	s, err := ParseSession(out)
	if err != nil {
		t.Fatalf("parse rewritten: %v", err)
	}
	if s.AccessToken != "new-acc" || s.RefreshToken != "new-ref" {
		t.Errorf("tokens not rotated: %+v", s)
	}
	if s.ExpiresAt != 1_000+604800 {
		t.Errorf("ExpiresAt = %d, want now + expires_in", s.ExpiresAt)
	}
	if s.UserID != "u1" || s.Email != "u1@example.com" {
		t.Errorf("user lost: %+v", s)
	}
	if !strings.Contains(string(out), "\n\t\"") {
		t.Error("session file should keep Wispr's tab indentation")
	}
}

func TestApplyRefresh_rejectsDifferentUser(t *testing.T) {
	t.Parallel()
	before := sessionFile(t, "a", "r", 100, "u1")
	tok := &TokenResponse{AccessToken: "x", RefreshToken: "y", ExpiresIn: 10, User: json.RawMessage(`{"id":"someone-else"}`)}
	if _, err := ApplyRefresh(before, tok, 0); err == nil {
		t.Error("a token set for another user must never be written")
	}
}

func TestApplyRefresh_rejectsIncompleteTokens(t *testing.T) {
	t.Parallel()
	if _, err := ApplyRefresh(sessionFile(t, "a", "r", 1, "u"), &TokenResponse{AccessToken: "only"}, 0); err == nil {
		t.Error("missing refresh token must be refused")
	}
}

func TestParseSnapshot_currentWeek(t *testing.T) {
	t.Parallel()
	s, err := ParseSnapshot(configFile(218125, 139.8, "2026-09-10T12:10:49.413Z"), fixedNow)
	if err != nil {
		t.Fatalf("ParseSnapshot: %v", err)
	}
	if s.TotalWords != 218125 || s.WordsPerMinute != 139.8 || s.AppCount != 2 {
		t.Errorf("got %+v", s)
	}
	if s.DayStreak != 3 || s.WeekStreak != 4 || s.WordsThisWeek != 120 {
		t.Errorf("fresh snapshot must keep its streaks: %+v", s)
	}
	if s.LastDictationAt != "2026-09-10T12:10:49Z" {
		t.Errorf("LastDictationAt = %q", s.LastDictationAt)
	}
}

func TestParseSnapshot_agesStaleValues(t *testing.T) {
	t.Parallel()
	s, err := ParseSnapshot(configFile(10, 100, "2026-08-01T12:00:00.000Z"), fixedNow)
	if err != nil {
		t.Fatalf("ParseSnapshot: %v", err)
	}
	if s.DayStreak != 0 || s.WeekStreak != 0 || s.WordsThisWeek != 0 {
		t.Errorf("a month-old snapshot cannot still be on a streak: %+v", s)
	}
	if s.TotalWords != 10 {
		t.Errorf("lifetime totals never age: %+v", s)
	}
}

func TestParseSnapshot_lastWeekKeepsWeekStreakOnly(t *testing.T) {
	t.Parallel()
	s, err := ParseSnapshot(configFile(10, 100, "2026-09-04T12:00:00.000Z"), fixedNow)
	if err != nil {
		t.Fatalf("ParseSnapshot: %v", err)
	}
	if s.DayStreak != 0 || s.WordsThisWeek != 0 {
		t.Errorf("day streak and this-week words must reset: %+v", s)
	}
	if s.WeekStreak != 4 {
		t.Errorf("a dictation last week keeps the week streak alive: %+v", s)
	}
}

func TestParseSnapshot_noStats(t *testing.T) {
	t.Parallel()
	if _, err := ParseSnapshot([]byte(`{"prefs":{"cache":{}}}`), fixedNow); err != ErrNoSnapshot {
		t.Errorf("err = %v, want ErrNoSnapshot", err)
	}
}

func TestParseAPIStats_withBreakdown(t *testing.T) {
	t.Parallel()
	s, err := ParseAPIStats([]byte(liveStatsBody))
	if err != nil {
		t.Fatalf("ParseAPIStats: %v", err)
	}
	if s.TotalWords != 5000 || s.DayStreak != 6 || s.AppCount != 3 {
		t.Errorf("got %+v", s)
	}
	if s.Desktop == nil || s.Desktop.TotalWords != 4000 || s.Mobile == nil || s.Mobile.WordsThisWeek != 200 {
		t.Errorf("breakdown missing: desktop=%+v mobile=%+v", s.Desktop, s.Mobile)
	}
}

func TestParseAPIStats_notInitialized(t *testing.T) {
	t.Parallel()
	if _, err := ParseAPIStats([]byte(`{"is_initialized":false}`)); err != ErrNotInitialized {
		t.Errorf("err = %v, want ErrNotInitialized", err)
	}
}

func TestSummarize_weightsWPMBySpeakingTime(t *testing.T) {
	t.Parallel()
	rows := []AccountReport{
		{Stats: &Stats{TotalWords: 100, WordsPerMinute: 100, SpeakingSeconds: 300, DayStreak: 2, WeekStreak: 5, WordsThisWeek: 10, LastDictationAt: "2026-09-01T00:00:00Z"}},
		{Stats: &Stats{TotalWords: 200, WordsPerMinute: 200, SpeakingSeconds: 100, DayStreak: 7, WeekStreak: 1, WordsThisWeek: 5, LastDictationAt: "2026-09-09T00:00:00Z"}},
		{Stats: nil},
	}
	got := Summarize(rows)
	if got.Accounts != 2 || got.TotalWords != 300 || got.WordsThisWeek != 15 || got.SpeakingSeconds != 400 {
		t.Errorf("got %+v", got)
	}
	if got.WordsPerMinute != 125 {
		t.Errorf("WordsPerMinute = %v, want 125 (weighted by speaking time)", got.WordsPerMinute)
	}
	if got.BestDayStreak != 7 || got.BestWeekStreak != 5 {
		t.Errorf("best streaks = %d/%d", got.BestDayStreak, got.BestWeekStreak)
	}
	if got.LastDictationAt != "2026-09-09T00:00:00Z" {
		t.Errorf("LastDictationAt = %q", got.LastDictationAt)
	}
}

type fakeWispr struct {
	server      *httptest.Server
	validTokens map[string]bool
	refreshes   map[string]*TokenResponse
	statsCalls  atomic.Int32
	tokenCalls  atomic.Int32
	lastAPIKey  atomic.Value
	lastAuthHdr atomic.Value
}

func newFakeWispr(t *testing.T) *fakeWispr {
	t.Helper()
	f := &fakeWispr{validTokens: map[string]bool{}, refreshes: map[string]*TokenResponse{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/history/stats", func(w http.ResponseWriter, r *http.Request) {
		f.statsCalls.Add(1)
		auth := r.Header.Get("Authorization")
		f.lastAuthHdr.Store(auth)
		if !f.validTokens[auth] {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"detail":"Invalid or expired token"}`)
			return
		}
		_, _ = io.WriteString(w, liveStatsBody)
	})
	mux.HandleFunc("/auth/v1/token", func(w http.ResponseWriter, r *http.Request) {
		f.tokenCalls.Add(1)
		f.lastAPIKey.Store(r.Header.Get("apikey"))
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		tok, ok := f.refreshes[body.RefreshToken]
		if !ok || r.URL.Query().Get("grant_type") != "refresh_token" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		delete(f.refreshes, body.RefreshToken)
		_ = json.NewEncoder(w).Encode(tok)
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeWispr) client() *Client {
	return &Client{
		HTTP:     f.server.Client(),
		StatsURL: f.server.URL + "/history/stats",
		TokenURL: f.server.URL + "/auth/v1/token?grant_type=refresh_token",
		AnonKey:  "anon-test",
	}
}

func TestClient_fetchStatsSendsRawToken(t *testing.T) {
	t.Parallel()
	f := newFakeWispr(t)
	f.validTokens["jwt-1"] = true
	s, err := f.client().FetchStats(context.Background(), "jwt-1")
	if err != nil {
		t.Fatalf("FetchStats: %v", err)
	}
	if s.TotalWords != 5000 {
		t.Errorf("got %+v", s)
	}
	if got := f.lastAuthHdr.Load(); got != "jwt-1" {
		t.Errorf("Authorization = %v, want the raw JWT with no Bearer prefix", got)
	}
}

func TestClient_fetchStatsUnauthorized(t *testing.T) {
	t.Parallel()
	f := newFakeWispr(t)
	if _, err := f.client().FetchStats(context.Background(), "nope"); err != ErrUnauthorized {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestClient_refreshSendsAnonKeyAndRejectsDeadToken(t *testing.T) {
	t.Parallel()
	f := newFakeWispr(t)
	f.refreshes["r1"] = &TokenResponse{AccessToken: "a2", RefreshToken: "r2", ExpiresIn: 604800}
	tok, err := f.client().Refresh(context.Background(), "r1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.AccessToken != "a2" || tok.RefreshToken != "r2" {
		t.Errorf("got %+v", tok)
	}
	if got := f.lastAPIKey.Load(); got != "anon-test" {
		t.Errorf("apikey = %v", got)
	}
	if _, err := f.client().Refresh(context.Background(), "r1"); err != ErrSessionExpired {
		t.Errorf("reused refresh token: err = %v, want ErrSessionExpired", err)
	}
}

func writeAccount(t *testing.T, dir string, session []byte, config []byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if session != nil {
		if err := os.WriteFile(filepath.Join(dir, SessionFileName), session, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if config != nil {
		if err := os.WriteFile(filepath.Join(dir, ConfigFileName), config, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCollect_snapshotOnlyMakesNoRequests(t *testing.T) {
	t.Parallel()
	f := newFakeWispr(t)
	dir := t.TempDir()
	writeAccount(t, dir, sessionFile(t, "a", "r", fixedNow.Unix()+3600, "u1"), configFile(42, 120, "2026-09-10T10:00:00.000Z"))

	c := &Collector{Client: f.client(), Now: func() time.Time { return fixedNow }}
	report := c.Collect(context.Background(), []Account{{UniqueID: "u1", DisplayName: "One", Dir: dir}}, false)

	if f.statsCalls.Load() != 0 || f.tokenCalls.Load() != 0 {
		t.Error("a snapshot read must not touch the network")
	}
	row := report.Accounts[0]
	if row.Source != SourceSnapshot || row.Stats == nil || row.Stats.TotalWords != 42 {
		t.Errorf("row = %+v", row)
	}
	if report.Totals.TotalWords != 42 || report.Live {
		t.Errorf("report = %+v", report)
	}
}

func TestCollect_liveWithValidToken(t *testing.T) {
	t.Parallel()
	f := newFakeWispr(t)
	f.validTokens["good"] = true
	dir := t.TempDir()
	writeAccount(t, dir, sessionFile(t, "good", "r", fixedNow.Unix()+3600, "u1"), configFile(42, 120, "2026-09-10T10:00:00.000Z"))

	c := &Collector{Client: f.client(), Now: func() time.Time { return fixedNow }}
	row := c.Collect(context.Background(), []Account{{UniqueID: "u1", Dir: dir}}, true).Accounts[0]

	if row.Source != SourceLive || row.Stats.TotalWords != 5000 || row.TokenRefreshed {
		t.Errorf("row = %+v", row)
	}
	if f.tokenCalls.Load() != 0 {
		t.Error("a valid token must not be refreshed")
	}
}

func TestCollect_expiredSavedAccountRefreshesAndPersistsRotation(t *testing.T) {
	t.Parallel()
	f := newFakeWispr(t)
	f.validTokens["fresh"] = true
	f.refreshes["old-ref"] = &TokenResponse{AccessToken: "fresh", RefreshToken: "new-ref", ExpiresIn: 604800}
	dir := t.TempDir()
	writeAccount(t, dir, sessionFile(t, "stale", "old-ref", fixedNow.Unix()-10, "u1"), nil)

	c := &Collector{Client: f.client(), Now: func() time.Time { return fixedNow }}
	row := c.Collect(context.Background(), []Account{{UniqueID: "u1", Dir: dir}}, true).Accounts[0]

	if row.Source != SourceLive || !row.TokenRefreshed || row.Problem != "" {
		t.Fatalf("row = %+v", row)
	}
	saved, err := os.ReadFile(filepath.Join(dir, SessionFileName))
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseSession(saved)
	if err != nil {
		t.Fatal(err)
	}
	if s.RefreshToken != "new-ref" || s.AccessToken != "fresh" {
		t.Errorf("rotated tokens were not written back: %+v", s)
	}
}

func TestCollect_currentAccountIsNeverRefreshed(t *testing.T) {
	t.Parallel()
	f := newFakeWispr(t)
	f.refreshes["live-ref"] = &TokenResponse{AccessToken: "x", RefreshToken: "y", ExpiresIn: 1}
	dir := t.TempDir()
	session := sessionFile(t, "stale", "live-ref", fixedNow.Unix()-10, "u1")
	writeAccount(t, dir, session, configFile(7, 90, "2026-09-10T10:00:00.000Z"))

	c := &Collector{Client: f.client(), Now: func() time.Time { return fixedNow }}
	row := c.Collect(context.Background(), []Account{{UniqueID: "u1", Current: true, Dir: dir}}, true).Accounts[0]

	if f.tokenCalls.Load() != 0 {
		t.Fatal("the account Wispr is signed into owns its refresh token; refreshing it would sign Wispr out")
	}
	if row.Problem != ProblemCurrentExpired || row.Source != SourceSnapshot || row.Stats.TotalWords != 7 {
		t.Errorf("row = %+v, want the snapshot kept with current_expired", row)
	}
	after, _ := os.ReadFile(filepath.Join(dir, SessionFileName))
	if string(after) != string(session) {
		t.Error("the live session file must not be touched")
	}
}

func TestCollect_deadRefreshTokenFallsBackToSnapshot(t *testing.T) {
	t.Parallel()
	f := newFakeWispr(t)
	dir := t.TempDir()
	session := sessionFile(t, "stale", "dead", fixedNow.Unix()-10, "u1")
	writeAccount(t, dir, session, configFile(9, 100, "2026-09-10T10:00:00.000Z"))

	c := &Collector{Client: f.client(), Now: func() time.Time { return fixedNow }}
	row := c.Collect(context.Background(), []Account{{UniqueID: "u1", Dir: dir}}, true).Accounts[0]

	if row.Problem != ProblemSessionExpired || row.Source != SourceSnapshot || row.Stats.TotalWords != 9 {
		t.Errorf("row = %+v", row)
	}
	after, _ := os.ReadFile(filepath.Join(dir, SessionFileName))
	if string(after) != string(session) {
		t.Error("a failed refresh must leave the saved session untouched")
	}
}

func TestCollect_failedWriteIsReportedAndStatsNotFetched(t *testing.T) {
	t.Parallel()
	f := newFakeWispr(t)
	f.validTokens["fresh"] = true
	f.refreshes["old"] = &TokenResponse{AccessToken: "fresh", RefreshToken: "new", ExpiresIn: 10}
	dir := t.TempDir()
	writeAccount(t, dir, sessionFile(t, "stale", "old", fixedNow.Unix()-10, "u1"), nil)

	c := &Collector{
		Client:    f.client(),
		Now:       func() time.Time { return fixedNow },
		WriteFile: func(string, []byte) error { return os.ErrPermission },
	}
	row := c.Collect(context.Background(), []Account{{UniqueID: "u1", Dir: dir}}, true).Accounts[0]

	if row.Problem != ProblemSessionFile {
		t.Errorf("row = %+v, want session_file", row)
	}
	if f.statsCalls.Load() != 0 {
		t.Error("stats must not be fetched with a token that could not be saved")
	}
}

func TestCollect_missingFilesReportNoData(t *testing.T) {
	t.Parallel()
	f := newFakeWispr(t)
	c := &Collector{Client: f.client(), Now: func() time.Time { return fixedNow }}
	row := c.Collect(context.Background(), []Account{{UniqueID: "u1", Dir: t.TempDir()}}, true).Accounts[0]
	if row.Stats != nil || row.Problem != ProblemSignedOut {
		t.Errorf("row = %+v", row)
	}
}
