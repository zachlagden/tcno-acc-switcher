package wisprsettings

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"TcNo-Acc-Switcher/internal/wisprstats"

	"github.com/tidwall/gjson"
)

var testNow = time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

func sessionFile(t *testing.T, access, refresh string, expiresAt int64, email string) []byte {
	t.Helper()
	inner, err := json.Marshal(map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"expires_at":    expiresAt,
		"user":          map[string]any{"id": "user-1", "email": email},
	})
	if err != nil {
		t.Fatal(err)
	}
	outer, err := json.Marshal(map[string]string{wisprstats.SessionKey: string(inner)})
	if err != nil {
		t.Fatal(err)
	}
	return outer
}

const baseConfig = `{"prefs":{"user":{"personalizationStyles":{"work":"formal","unset":"default"},"autoCleanup":{"level":"light","diffShownCount":2,"migrated":true},"userVoicesMigrated":false,"avatarUrl":"x"},"cache":{"syncedPrefsModifiedAt":"old","prefsDirty":true,"statistics":{"totalWords":5}}}}`

func accountDir(t *testing.T, access string, expiresAt int64, config string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, wisprstats.SessionFileName), sessionFile(t, access, "refresh-1", expiresAt, "me@example.com"), 0o600); err != nil {
		t.Fatal(err)
	}
	if config != "" {
		if err := os.WriteFile(filepath.Join(dir, wisprstats.ConfigFileName), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type fakeWispr struct {
	mu            sync.Mutex
	prefs         map[string]any
	modifiedAt    []string
	getCount      int
	postStatus    []int
	prefsPosts    []map[string]any
	dictionary    []map[string]any
	dictPosts     [][]map[string]any
	authSeen      []string
	tokenCalls    int
	rejectToken   string
	refreshedWith string
}

func (f *fakeWispr) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/token" {
			f.tokenCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  f.refreshedWith,
				"refresh_token": "refresh-2",
				"expires_in":    604800,
				"expires_at":    testNow.Unix() + 604800,
			})
			return
		}
		auth := r.Header.Get("Authorization")
		f.authSeen = append(f.authSeen, auth)
		if auth == f.rejectToken || strings.HasPrefix(auth, "Bearer") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.URL.Path == "/prefs" && r.Method == http.MethodGet:
			idx := min(f.getCount, len(f.modifiedAt)-1)
			f.getCount++
			_ = json.NewEncoder(w).Encode(map[string]any{"user_id": "user-1", "preferences": f.prefs, "modified_at": f.modifiedAt[idx]})
		case r.URL.Path == "/prefs" && r.Method == http.MethodPost:
			var parsed map[string]any
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Errorf("prefs POST body is not JSON: %v", err)
			}
			f.prefsPosts = append(f.prefsPosts, parsed)
			status := http.StatusOK
			if len(f.postStatus) > 0 {
				status = f.postStatus[0]
				f.postStatus = f.postStatus[1:]
			}
			if status != http.StatusOK {
				w.WriteHeader(status)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"modified_at": "2026-09-13T12:00:01.000Z"})
		case r.URL.Path == "/dict" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(f.dictionary)
		case r.URL.Path == "/dict" && r.Method == http.MethodPost:
			var items []map[string]any
			if err := json.Unmarshal(body, &items); err != nil {
				t.Errorf("dictionary POST body is not a bare array: %v", err)
			}
			f.dictPosts = append(f.dictPosts, items)
			for _, it := range items {
				replaced := false
				for i, existing := range f.dictionary {
					if existing["id"] == it["id"] {
						f.dictionary[i] = it
						replaced = true
					}
				}
				if !replaced {
					f.dictionary = append(f.dictionary, it)
				}
			}
			_ = json.NewEncoder(w).Encode(items)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func newTestSyncer(t *testing.T, f *fakeWispr) *Syncer {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	ids := 0
	return &Syncer{
		Client: &Client{HTTP: srv.Client(), PrefsURL: srv.URL + "/prefs", DictionaryURL: srv.URL + "/dict"},
		Tokens: &wisprstats.Client{HTTP: srv.Client(), TokenURL: srv.URL + "/token", AnonKey: "anon"},
		Now:    func() time.Time { return testNow },
		NewID: func() string {
			ids++
			return "new-id-" + string(rune('0'+ids))
		},
		WriteFile: func(path string, data []byte) error { return os.WriteFile(path, data, 0o600) },
	}
}

func serverPrefs() map[string]any {
	return map[string]any{
		"personalization_styles":             map[string]any{"work": "formal", "email": "formal", "unset": "default"},
		"auto_cleanup_level":                 "light",
		"polish_instructions":                map[string]any{"default": "keep it short"},
		"output_languages":                   []any{"en"},
		"personalizationOnboardingCompleted": true,
	}
}

func stylesProfile() Profile {
	p := DefaultProfile()
	p.Parts = Parts{Styles: true, AutoCleanup: true}
	p.Styles = Styles{Work: "casual", Personal: "veryCasual"}
	p.AutoCleanupLevel = "high"
	return p
}

func TestMergePrefsReplacesOnlyStylesAndCleanup(t *testing.T) {
	raw, _ := json.Marshal(serverPrefs())
	var prefs map[string]json.RawMessage
	_ = json.Unmarshal(raw, &prefs)
	merge, err := MergePrefs(ServerPrefs{Preferences: prefs, ModifiedAt: "2026-09-01T00:00:00Z"}, stylesProfile())
	if err != nil {
		t.Fatal(err)
	}
	if !merge.Changed {
		t.Fatal("expected a change")
	}
	body := gjson.ParseBytes(merge.Body)
	checks := map[string]string{
		"modified_at": "2026-09-01T00:00:00Z",
		"preferences.personalization_styles.work":        "casual",
		"preferences.personalization_styles.email":       "formal",
		"preferences.personalization_styles.personal":    "veryCasual",
		"preferences.personalization_styles.unset":       "default",
		"preferences.auto_cleanup_level":                 "high",
		"preferences.polish_instructions.default":        "keep it short",
		"preferences.output_languages.0":                 "en",
		"preferences.personalizationOnboardingCompleted": "true",
	}
	for path, want := range checks {
		if got := body.Get(path).String(); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if body.Get("preferences.personalization_styles.other").Exists() {
		t.Error("an unset category was written")
	}
}

func TestMergePrefsUnchanged(t *testing.T) {
	p := stylesProfile()
	p.Styles = Styles{Work: "formal"}
	p.AutoCleanupLevel = "light"
	raw, _ := json.Marshal(serverPrefs())
	var prefs map[string]json.RawMessage
	_ = json.Unmarshal(raw, &prefs)
	merge, err := MergePrefs(ServerPrefs{Preferences: prefs, ModifiedAt: "x"}, p)
	if err != nil {
		t.Fatal(err)
	}
	if merge.Changed {
		t.Fatal("expected no change")
	}
}

func TestApplyPrefsRetriesConflictOnceAndUpdatesCache(t *testing.T) {
	f := &fakeWispr{prefs: serverPrefs(), modifiedAt: []string{"m1", "m2"}, postStatus: []int{http.StatusConflict}}
	s := newTestSyncer(t, f)
	dir := accountDir(t, "tok-a", testNow.Unix()+3600, baseConfig)
	res := s.Apply(context.Background(), wisprstats.Account{UniqueID: "u1", Dir: dir}, stylesProfile(), ApplyOptions{AllowRefresh: true, LocalConfigWritable: true})
	if res.Prefs.Status != StatusApplied || res.Prefs.Problem != "" {
		t.Fatalf("prefs result = %+v", res.Prefs)
	}
	if len(f.prefsPosts) != 2 {
		t.Fatalf("posts = %d, want 2", len(f.prefsPosts))
	}
	if f.prefsPosts[0]["modified_at"] != "m1" || f.prefsPosts[1]["modified_at"] != "m2" {
		t.Fatalf("modified_at sent = %v then %v", f.prefsPosts[0]["modified_at"], f.prefsPosts[1]["modified_at"])
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, wisprstats.ConfigFileName))
	checks := map[string]string{
		"prefs.user.personalizationStyles.work":     "casual",
		"prefs.user.personalizationStyles.personal": "veryCasual",
		"prefs.user.personalizationStyles.unset":    "default",
		"prefs.user.autoCleanup.level":              "high",
		"prefs.user.autoCleanup.diffShownCount":     "2",
		"prefs.cache.syncedPrefsModifiedAt":         "2026-09-13T12:00:01.000Z",
		"prefs.cache.prefsDirty":                    "false",
		"prefs.cache.statistics.totalWords":         "5",
		"prefs.user.avatarUrl":                      "x",
	}
	for path, want := range checks {
		if got := gjson.GetBytes(cfg, path).String(); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestApplyPrefsGivesUpAfterSecondConflict(t *testing.T) {
	f := &fakeWispr{prefs: serverPrefs(), modifiedAt: []string{"m1"}, postStatus: []int{http.StatusConflict, http.StatusConflict}}
	s := newTestSyncer(t, f)
	dir := accountDir(t, "tok-a", testNow.Unix()+3600, baseConfig)
	res := s.Apply(context.Background(), wisprstats.Account{Dir: dir}, stylesProfile(), ApplyOptions{AllowRefresh: true, LocalConfigWritable: true})
	if res.Prefs.Status != StatusFailed || res.Prefs.Problem != ProblemConflict {
		t.Fatalf("prefs result = %+v", res.Prefs)
	}
	if len(f.prefsPosts) != 2 {
		t.Fatalf("posts = %d, want 2", len(f.prefsPosts))
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, wisprstats.ConfigFileName))
	if string(cfg) != baseConfig {
		t.Fatal("config.json changed after a failed apply")
	}
}

func TestApplyRefreshesExpiredSavedSession(t *testing.T) {
	f := &fakeWispr{prefs: serverPrefs(), modifiedAt: []string{"m1"}, refreshedWith: "tok-new"}
	s := newTestSyncer(t, f)
	dir := accountDir(t, "tok-old", testNow.Unix()-10, baseConfig)
	res := s.Apply(context.Background(), wisprstats.Account{Dir: dir}, stylesProfile(), ApplyOptions{AllowRefresh: true, LocalConfigWritable: true})
	if res.Prefs.Status != StatusApplied || !res.TokenRefreshed {
		t.Fatalf("result = %+v", res)
	}
	for _, a := range f.authSeen {
		if a != "tok-new" {
			t.Fatalf("API saw token %q, want only the refreshed one", a)
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, wisprstats.SessionFileName))
	session, err := wisprstats.ParseSession(data)
	if err != nil || session.AccessToken != "tok-new" || session.RefreshToken != "refresh-2" {
		t.Fatalf("session not rotated on disk: %+v %v", session, err)
	}
}

func TestApplyNeverRefreshesLiveSession(t *testing.T) {
	f := &fakeWispr{prefs: serverPrefs(), modifiedAt: []string{"m1"}, refreshedWith: "tok-new"}
	s := newTestSyncer(t, f)
	dir := accountDir(t, "tok-old", testNow.Unix()-10, baseConfig)
	p := stylesProfile()
	p.Parts.Dictionary = true
	res := s.Apply(context.Background(), wisprstats.Account{Dir: dir, Current: true}, p, ApplyOptions{})
	if res.Prefs.Problem != ProblemCurrentExpired || res.Dictionary.Problem != ProblemCurrentExpired {
		t.Fatalf("result = %+v", res)
	}
	if f.tokenCalls != 0 {
		t.Fatal("refreshed the live session")
	}
}

func planWords(t *testing.T, remote, local []RemoteItem, words ...DictionaryEntry) DictionaryPlan {
	t.Helper()
	n := 0
	plan, err := PlanDictionary(remote, local, words, testNow, func() string { n++; return "id-" + string(rune('0'+n)) })
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range plan.Upserts {
		if itemBool(it, "is_deleted") {
			t.Fatalf("upsert deletes %s", itemString(it, "word"))
		}
	}
	return plan
}

func TestPlanDictionaryIgnoresCaseAndSpacing(t *testing.T) {
	remote := []RemoteItem{item(t, map[string]any{"id": "r1", "team_dictionary_id": PersonalTeamID, "word": "Wispr Flow", "is_deleted": false})}
	plan := planWords(t, remote, nil, DictionaryEntry{Word: "wispr flow"}, DictionaryEntry{Word: " Wispr  Flow "}, DictionaryEntry{Word: "WISPR\tFLOW"})
	if len(plan.Upserts) != 0 {
		t.Fatalf("re-added a variant: %v", plan.Upserts)
	}
}

func TestPlanDictionaryNeverAddsTeamWords(t *testing.T) {
	remote := []RemoteItem{item(t, map[string]any{"id": "r4", "team_dictionary_id": "11111111-1111-1111-1111-111111111111", "word": "TeamWord", "is_deleted": false})}
	if plan := planWords(t, remote, nil, DictionaryEntry{Word: "teamword"}); len(plan.Upserts) != 0 {
		t.Fatalf("added a personal copy of a team word: %v", plan.Upserts)
	}
}

func TestPlanDictionaryLeavesExistingEntriesUntouched(t *testing.T) {
	remote := []RemoteItem{item(t, map[string]any{"id": "r2", "team_dictionary_id": PersonalTeamID, "word": "brb", "replacement": "be back", "is_snippet": true, "is_deleted": false})}
	plan := planWords(t, remote, nil, DictionaryEntry{Word: "BRB", Replacement: "be right back", IsSnippet: false})
	if len(plan.Upserts) != 0 {
		t.Fatalf("modified an existing entry: %v", plan.Upserts)
	}
}

func TestPlanDictionaryRestoresNewestTombstoneOnce(t *testing.T) {
	remote := []RemoteItem{
		item(t, map[string]any{"id": "old", "team_dictionary_id": PersonalTeamID, "word": "Revived", "replacement": "x", "is_deleted": true, "modified_at": "2026-01-01T00:00:00.000Z"}),
		item(t, map[string]any{"id": "new", "team_dictionary_id": PersonalTeamID, "word": "revived ", "replacement": "y", "is_deleted": true, "modified_at": "2026-05-01T00:00:00.000Z"}),
	}
	plan := planWords(t, remote, nil, DictionaryEntry{Word: "REVIVED", Replacement: "z"}, DictionaryEntry{Word: "revived"})
	if len(plan.Upserts) != 1 || plan.Restored != 1 || plan.Added != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	got := plan.Upserts[0]
	if itemString(got, "id") != "new" || itemString(got, "word") != "revived " || itemString(got, "replacement") != "y" {
		t.Fatalf("restored the wrong row or changed it: %v", got)
	}
	if itemString(got, "modified_at") != "2026-09-13T12:00:00.000Z" {
		t.Fatalf("modified_at = %s", got["modified_at"])
	}
}

func TestPlanDictionaryAddsMissingWordOnce(t *testing.T) {
	plan := planWords(t, nil, nil, DictionaryEntry{Word: " Fresh  Co ", Replacement: "Fresh Co", ReplacementHTML: "<b>Fresh</b>"}, DictionaryEntry{Word: "fresh co"})
	if plan.Added != 1 || len(plan.Upserts) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	fresh := plan.Upserts[0]
	want := map[string]string{
		"id": `"id-1"`, "word": `"Fresh Co"`, "team_dictionary_id": `"` + PersonalTeamID + `"`, "is_manual": "true", "source": `"manual"`,
		"is_deleted": "false", "is_snippet": "false", "replacement": `"Fresh Co"`,
		"created_at": `"2026-09-13T12:00:00.000Z"`, "modified_at": `"2026-09-13T12:00:00.000Z"`, "frequency_used": "0", "last_used": "null",
	}
	for k, v := range want {
		if string(fresh[k]) != v {
			t.Errorf("new item %s = %s, want %s", k, fresh[k], v)
		}
	}
	if itemString(fresh, "replacement_html") != "<b>Fresh</b>" {
		t.Errorf("replacement_html = %s", fresh["replacement_html"])
	}
}

func TestApplyDictionarySkipsLocalOnlyRowsAndIsIdempotent(t *testing.T) {
	f := &fakeWispr{}
	s := newTestSyncer(t, f)
	dir := accountDir(t, "tok-a", testNow.Unix()+3600, "")
	db, err := os.ReadFile(filepath.Join("testdata", FlowDBFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FlowDBFileName), db, 0o600); err != nil {
		t.Fatal(err)
	}
	p := DefaultProfile()
	p.Parts.Dictionary = true
	p.Dictionary = []DictionaryEntry{{Word: "kubernetes"}, {Word: "BRB", Replacement: "other", IsSnippet: true}, {Word: "TEAMWORD"}, {Word: "Gone"}, {Word: "New Word"}}
	opts := ApplyOptions{AllowRefresh: true, LocalConfigWritable: true}
	res := s.Apply(context.Background(), wisprstats.Account{Dir: dir}, p, opts)
	if res.Dictionary.Status != StatusApplied || res.Dictionary.Added != 2 || len(f.dictPosts) != 1 {
		t.Fatalf("first apply = %+v posts=%v", res.Dictionary, f.dictPosts)
	}
	words := []string{}
	for _, it := range f.dictPosts[0] {
		words = append(words, it["word"].(string))
	}
	if strings.Join(words, ",") != "Gone,New Word" {
		t.Fatalf("posted words = %v", words)
	}
	res = s.Apply(context.Background(), wisprstats.Account{Dir: dir}, p, opts)
	if res.Dictionary.Status != StatusUnchanged || len(f.dictPosts) != 1 {
		t.Fatalf("second apply = %+v posts=%d", res.Dictionary, len(f.dictPosts))
	}
}

func TestImportCollapsesDuplicateWords(t *testing.T) {
	remote := []RemoteItem{
		item(t, map[string]any{"word": "Wispr Flow", "source": "manual", "team_dictionary_id": PersonalTeamID}),
		item(t, map[string]any{"word": " wispr  flow", "source": "user_edits", "team_dictionary_id": PersonalTeamID}),
	}
	entries, excluded := ImportableEntries(remote, "")
	if len(entries) != 1 || entries[0].Word != "Wispr Flow" || excluded != 0 {
		t.Fatalf("entries = %+v excluded=%d", entries, excluded)
	}
}

func TestApplyDictionarySkipsPostWhenNothingChanged(t *testing.T) {
	f := &fakeWispr{dictionary: []map[string]any{{"id": "r1", "team_dictionary_id": PersonalTeamID, "word": "Kubernetes", "is_deleted": false}}}
	s := newTestSyncer(t, f)
	dir := accountDir(t, "tok-a", testNow.Unix()+3600, "")
	p := DefaultProfile()
	p.Parts.Dictionary = true
	p.Dictionary = []DictionaryEntry{{Word: "Kubernetes"}}
	res := s.Apply(context.Background(), wisprstats.Account{Dir: dir}, p, ApplyOptions{AllowRefresh: true, LocalConfigWritable: true})
	if res.Dictionary.Status != StatusUnchanged || len(f.dictPosts) != 0 {
		t.Fatalf("result = %+v posts=%d", res.Dictionary, len(f.dictPosts))
	}
	p.Dictionary = append(p.Dictionary, DictionaryEntry{Word: "Wispr"})
	res = s.Apply(context.Background(), wisprstats.Account{Dir: dir}, p, ApplyOptions{AllowRefresh: true, LocalConfigWritable: true})
	if res.Dictionary.Status != StatusApplied || res.Dictionary.Added != 1 || len(f.dictPosts) != 1 || len(f.dictPosts[0]) != 1 {
		t.Fatalf("result = %+v posts=%v", res.Dictionary, f.dictPosts)
	}
}

func TestApplyVoices(t *testing.T) {
	voices := map[string]Voice{"work": {ID: "work", Source: "builtIn", Name: "Work messages", AppNames: []string{"Slack"}, AutoCleanupLevel: "high", StylePreference: "casual"}}
	p := DefaultProfile()
	p.Parts.Voices = true
	p.UserVoices = voices
	s := newTestSyncer(t, &fakeWispr{})
	dir := accountDir(t, "tok-a", testNow.Unix()+3600, baseConfig)

	res := s.Apply(context.Background(), wisprstats.Account{Dir: dir, Current: true}, p, ApplyOptions{})
	if res.Voices.Problem != ProblemWisprRunning {
		t.Fatalf("voices while running = %+v", res.Voices)
	}
	res = s.Apply(context.Background(), wisprstats.Account{Dir: dir}, p, ApplyOptions{LocalConfigWritable: true})
	if res.Voices.Status != StatusApplied {
		t.Fatalf("voices = %+v", res.Voices)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, wisprstats.ConfigFileName))
	if gjson.GetBytes(cfg, "prefs.user.userVoices.work.stylePreference").String() != "casual" || !gjson.GetBytes(cfg, "prefs.user.userVoicesMigrated").Bool() {
		t.Fatalf("voices not written: %s", cfg)
	}
	res = s.Apply(context.Background(), wisprstats.Account{Dir: dir}, p, ApplyOptions{LocalConfigWritable: true})
	if res.Voices.Status != StatusUnchanged {
		t.Fatalf("second apply = %+v", res.Voices)
	}
}

func TestImportFromServerExcludesAccountSpecificRows(t *testing.T) {
	f := &fakeWispr{prefs: serverPrefs(), modifiedAt: []string{"m1"}, dictionary: []map[string]any{
		{"word": "my email address", "replacement": "me@example.com", "source": "default", "is_snippet": true, "team_dictionary_id": PersonalTeamID},
		{"word": "my Flow referral", "replacement": "https://wisprflow.ai/r?ME", "source": "default", "is_snippet": true, "team_dictionary_id": PersonalTeamID},
		{"word": "work mail", "replacement": "Reach me at ME@example.com", "source": "manual", "is_snippet": true, "team_dictionary_id": PersonalTeamID},
		{"word": "deleted", "source": "manual", "is_deleted": true, "team_dictionary_id": PersonalTeamID},
		{"word": "team", "source": "manual", "team_dictionary_id": "11111111-1111-1111-1111-111111111111"},
		{"word": "Kubernetes", "source": "user_edits", "team_dictionary_id": PersonalTeamID},
		{"word": "sig", "replacement": "Thanks, Z", "replacement_html": "<p>Thanks, Z</p>", "source": "manual", "is_snippet": true, "team_dictionary_id": PersonalTeamID},
	}}
	s := newTestSyncer(t, f)
	dir := accountDir(t, "tok-a", testNow.Unix()+3600, `{"prefs":{"user":{"userVoices":{"work":{"id":"work","source":"builtIn","name":"Work","appNames":["Slack"],"autoCleanupLevel":"light","stylePreference":"formal"}}}}}`)
	res := s.Import(context.Background(), wisprstats.Account{DisplayName: "me@example.com", Dir: dir}, true, true)
	if res.PrefsSource != SourceServer || res.DictionarySource != SourceServer || res.Problem != "" {
		t.Fatalf("sources = %+v", res)
	}
	if res.Excluded != 3 {
		t.Fatalf("excluded = %d, want 3", res.Excluded)
	}
	words := []string{}
	for _, e := range res.Profile.Dictionary {
		words = append(words, e.Word)
	}
	if strings.Join(words, ",") != "Kubernetes,sig" {
		t.Fatalf("imported words = %v", words)
	}
	if res.Profile.Styles.Work != "formal" || res.Profile.AutoCleanupLevel != "light" || !res.VoicesFound {
		t.Fatalf("profile = %+v", res.Profile)
	}
	if res.Profile.ImportedFrom == nil || res.Profile.ImportedFrom.DisplayName != "me@example.com" {
		t.Fatal("importedFrom missing")
	}
}

func TestImportOfflineUsesLocalFiles(t *testing.T) {
	s := newTestSyncer(t, &fakeWispr{})
	dir := accountDir(t, "tok-a", testNow.Unix()+3600, baseConfig)
	db, err := os.ReadFile(filepath.Join("testdata", FlowDBFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FlowDBFileName), db, 0o600); err != nil {
		t.Fatal(err)
	}
	res := s.Import(context.Background(), wisprstats.Account{Dir: dir}, false, false)
	if res.PrefsSource != SourceLocal || res.DictionarySource != SourceLocal {
		t.Fatalf("sources = %+v", res)
	}
	if res.Profile.Styles.Work != "formal" || res.Profile.AutoCleanupLevel != "light" {
		t.Fatalf("styles = %+v", res.Profile)
	}
	got := map[string]DictionaryEntry{}
	for _, e := range res.Profile.Dictionary {
		got[e.Word] = e
	}
	if len(got) != 2 || got["brb"].Replacement != "be right back" || !got["brb"].IsSnippet || got["brb"].ReplacementHTML != "<p>be right back</p>" {
		t.Fatalf("dictionary = %+v", res.Profile.Dictionary)
	}
	if _, ok := got["Kubernetes"]; !ok {
		t.Fatal("manual word missing")
	}
	if res.Excluded != 1 {
		t.Fatalf("excluded = %d, want 1", res.Excluded)
	}
}

func TestProfileStoreRoundTripAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), ProfileFileName)
	p, err := LoadProfile(path)
	if err != nil || p.Target != TargetAll {
		t.Fatalf("default profile = %+v %v", p, err)
	}
	p = stylesProfile()
	p.Target = TargetSelected
	p.SelectedAccounts = []string{" a ", "a", "b", ""}
	p.Dictionary = []DictionaryEntry{{Word: " x "}, {Word: ""}, {Word: "X", Replacement: "y"}, {Word: "Two  Words"}, {Word: "two words"}}
	write := func(path string, data []byte) error { return os.WriteFile(path, data, 0o600) }
	saved, err := SaveProfile(path, p, write)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(saved.SelectedAccounts, ",") != "a,b" || len(saved.Dictionary) != 2 || saved.Dictionary[0].Word != "x" || saved.Dictionary[0].Replacement != "" || saved.Dictionary[1].Word != "Two  Words" {
		t.Fatalf("normalized = %+v", saved)
	}
	loaded, err := LoadProfile(path)
	if err != nil || loaded.Styles.Work != "casual" || loaded.AutoCleanupLevel != "high" || loaded.Target != TargetSelected {
		t.Fatalf("loaded = %+v %v", loaded, err)
	}

	bad := []func(*Profile){
		func(p *Profile) { p.Styles.Email = "shouty" },
		func(p *Profile) { p.AutoCleanupLevel = "max" },
		func(p *Profile) { p.Target = "some" },
		func(p *Profile) { p.Dictionary = []DictionaryEntry{{Word: "s", IsSnippet: true}} },
		func(p *Profile) { p.Parts.Voices = true; p.UserVoices = nil },
		func(p *Profile) { p.UserVoices = map[string]Voice{"v": {StylePreference: "loud"}} },
	}
	for i, mutate := range bad {
		q := stylesProfile()
		mutate(&q)
		if _, err := SaveProfile(path, q, write); err == nil {
			t.Errorf("case %d: expected a validation error", i)
		}
	}
}

func TestAppliesOnSwitchToGating(t *testing.T) {
	p := stylesProfile()
	p.ApplyOnSwitch = true
	if !p.AppliesOnSwitchTo("a", false) {
		t.Fatal("expected apply")
	}
	if p.AppliesOnSwitchTo("a", true) {
		t.Fatal("applied while offline")
	}
	p.Target = TargetSelected
	p.SelectedAccounts = []string{"b"}
	if p.AppliesOnSwitchTo("a", false) || !p.AppliesOnSwitchTo("b", false) {
		t.Fatal("target selection ignored")
	}
	p.ApplyOnSwitch = false
	if p.AppliesOnSwitchTo("b", false) {
		t.Fatal("applied with apply on switch off")
	}
	p.ApplyOnSwitch = true
	p.Parts = Parts{}
	if p.AppliesOnSwitchTo("b", false) {
		t.Fatal("applied with every part off")
	}
}

func item(t *testing.T, m map[string]any) RemoteItem {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var out RemoteItem
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
