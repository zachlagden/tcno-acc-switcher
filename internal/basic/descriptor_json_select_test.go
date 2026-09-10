package basic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"TcNo-Acc-Switcher/internal/platform"
)

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func nestedSessionFixture(t *testing.T, key, id, email string) string {
	t.Helper()
	inner, err := json.Marshal(map[string]any{"user": map[string]string{"id": id, "email": email}})
	if err != nil {
		t.Fatal(err)
	}
	outer, err := json.Marshal(map[string]string{key: string(inner)})
	if err != nil {
		t.Fatal(err)
	}
	return string(outer)
}

func TestResolveJSONSelectValue_readsNestedJSONString(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "session.json")
	writeFixture(t, path, nestedSessionFixture(t, "auth", "u-1", "one@example.com"))

	got, handled, err := resolveJSONSelectValue("JSON_SELECT::"+path+"::auth|@fromstr|user.email", "", platform.PathTokenContext{})
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if got != "one@example.com" {
		t.Errorf("got %q, want one@example.com", got)
	}
}

func TestResolveJSONSelectValue_ignoresOtherSources(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"SQLITE:x.db|SELECT 1", "leveldb:x:y", "JSON_SELECT_FIRST,::x.json::a", "%userid%"} {
		if _, handled, _ := resolveJSONSelectValue(v, "", platform.PathTokenContext{}); handled {
			t.Errorf("%q must not be handled as a JSON_SELECT value", v)
		}
	}
}

func TestResolveJSONSelectValue_missingFileIsAnError(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "absent.json")
	_, handled, err := resolveJSONSelectValue("JSON_SELECT::"+path+"::a", "", platform.PathTokenContext{})
	if !handled || err == nil {
		t.Errorf("handled=%v err=%v, want handled with an error", handled, err)
	}
}

func TestResolveDescriptorVariables_jsonSelectLiveAndSaved(t *testing.T) {
	t.Parallel()
	liveDir := t.TempDir()
	livePath := filepath.Join(liveDir, "session.json")
	writeFixture(t, livePath, nestedSessionFixture(t, "auth", "live-id", "live@example.com"))

	savedRoot := t.TempDir()
	writeFixture(t, filepath.Join(savedRoot, "session.json"), nestedSessionFixture(t, "auth", "saved-id", "saved@example.com"))

	d := platform.Descriptor{
		LoginFiles: map[string]string{livePath: "session.json"},
		Extras: platform.DescriptorExtras{
			Variables: map[string]string{
				"email": "JSON_SELECT::" + livePath + "::auth|@fromstr|user.email",
			},
		},
	}

	live := resolveDescriptorVariables(d, "", platform.PathTokenContext{}, "", false)
	if live["email"] != "live@example.com" {
		t.Errorf("live email = %q, want live@example.com", live["email"])
	}

	saved := resolveDescriptorVariables(d, "", platform.PathTokenContext{}, savedRoot, true)
	if saved["email"] != "saved@example.com" {
		t.Errorf("saved email = %q, want saved@example.com", saved["email"])
	}

	emptyRoot := t.TempDir()
	missing := resolveDescriptorVariables(d, "", platform.PathTokenContext{}, emptyRoot, true)
	if missing["email"] != "" {
		t.Errorf("saved account without the file resolved %q, want empty rather than the live account", missing["email"])
	}
}

func TestResolveDescriptorVariables_jsonSelectOutsideLoginFilesStaysEmptyWhenSaved(t *testing.T) {
	t.Parallel()
	liveDir := t.TempDir()
	livePath := filepath.Join(liveDir, "session.json")
	writeFixture(t, livePath, nestedSessionFixture(t, "auth", "live-id", "live@example.com"))

	d := platform.Descriptor{
		Extras: platform.DescriptorExtras{
			Variables: map[string]string{"email": "JSON_SELECT::" + livePath + "::auth|@fromstr|user.email"},
		},
	}
	saved := resolveDescriptorVariables(d, "", platform.PathTokenContext{}, t.TempDir(), true)
	if saved["email"] != "" {
		t.Errorf("got %q; a saved account must never read the live file", saved["email"])
	}
}

func TestResolveDescriptorValue_jsonSelect(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "session.json")
	writeFixture(t, path, nestedSessionFixture(t, "auth", "u-42", "x@example.com"))

	got := resolveDescriptorValue(platform.Descriptor{}, "JSON_SELECT::"+path+"::auth|@fromstr|user.id", "", platform.PathTokenContext{}, nil, "", false)
	if got != "u-42" {
		t.Errorf("got %q, want u-42", got)
	}
}
