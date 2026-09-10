package basic

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"TcNo-Acc-Switcher/internal/platform"
)

const wisprPlatformKey = "Wispr Flow"
const wisprSessionKey = "sb-dodjkfqhwrzqjwkfnthl-auth-token"

func loadShippedDescriptor(t *testing.T, catalog, key string) platform.Descriptor {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", catalog))
	if err != nil {
		t.Fatalf("read %s: %v", catalog, err)
	}
	d, err := loadDescriptor(raw, key)
	if err != nil {
		t.Fatalf("load %s from %s: %v", key, catalog, err)
	}
	return d
}

func TestWisprFlowCatalog_descriptorShape(t *testing.T) {
	t.Parallel()
	for _, catalog := range []string{"Platforms.json", "Platforms.mac.json"} {
		d := loadShippedDescriptor(t, catalog, wisprPlatformKey)
		if !d.ExitBeforeSave || !d.ExitBeforeInteract {
			t.Errorf("%s: Wispr Flow holds its files open, so it must exit before save and interact", catalog)
		}
		if d.AllFilesRequired {
			t.Errorf("%s: the sqlite sidecars are not always present, so AllFilesRequired must be off", catalog)
		}
		wantFiles := []string{"session.json", "config.json", "flow.sqlite"}
		for _, want := range wantFiles {
			if !slices.Contains(mapValues(d.LoginFiles), want) {
				t.Errorf("%s: LoginFiles does not save %s", catalog, want)
			}
		}
		if len(d.PathListToClear) != len(d.LoginFiles) {
			t.Errorf("%s: Add New must clear exactly the saved login files", catalog)
		}
	}

	win := loadShippedDescriptor(t, "Platforms.json", wisprPlatformKey)
	for _, exe := range []string{"Wispr Flow.exe", "Wispr Flow Helper.exe"} {
		if !slices.Contains(win.ExesToEnd, exe) {
			t.Errorf("ExesToEnd is missing %s", exe)
		}
	}
	if win.Extras.ClosingMethod != "Close" || !win.Extras.ForceClosingMethod {
		t.Errorf("ClosingMethod = %q forced=%v; Electron's Alt+F4 only hides a tray app, so it must be a forced Close", win.Extras.ClosingMethod, win.Extras.ForceClosingMethod)
	}
	if win.Extras.QuitArgs != "--quit-app" {
		t.Errorf("QuitArgs = %q, want --quit-app so Wispr quits itself instead of being force-killed", win.Extras.QuitArgs)
	}
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func TestWisprFlowCatalog_resolvesLiveAndSavedAccounts(t *testing.T) {
	appData := t.TempDir()
	t.Setenv("AppData", appData)
	liveDir := filepath.Join(appData, "Wispr Flow")
	writeFixture(t, filepath.Join(liveDir, "session.json"), nestedSessionFixture(t, wisprSessionKey, "live-uuid", "live@example.com"))
	writeFixture(t, filepath.Join(liveDir, "config.json"), `{"prefs":{"user":{"avatarUrl":"https://example.com/live.png"}}}`)

	d := loadShippedDescriptor(t, "Platforms.json", wisprPlatformKey)
	ctx := platform.PathTokenContext{}

	id, err := ReadUniqueID(wisprPlatformKey, d, "")
	if err != nil {
		t.Fatalf("ReadUniqueID: %v", err)
	}
	if id != "live-uuid" {
		t.Errorf("unique id = %q, want live-uuid", id)
	}

	live := resolveDescriptorVariables(d, "", ctx, "", false)
	if got := resolveDescriptorValue(d, d.Extras.BuiltInUsernameFile, "", ctx, live, "", false); got != "live@example.com" {
		t.Errorf("suggested name = %q, want live@example.com", got)
	}
	if got := descriptorBuiltInProfileSource(d, "", ctx, live, "", false); got.RemoteURL != "https://example.com/live.png" {
		t.Errorf("profile image = %+v, want the live avatar URL", got)
	}

	savedRoot := t.TempDir()
	writeFixture(t, filepath.Join(savedRoot, "session.json"), nestedSessionFixture(t, wisprSessionKey, "saved-uuid", "saved@example.com"))
	writeFixture(t, filepath.Join(savedRoot, "config.json"), `{"prefs":{"user":{"avatarUrl":""}}}`)

	saved := resolveDescriptorVariables(d, "", ctx, savedRoot, true)
	if saved["userid"] != "saved-uuid" || saved["useremail"] != "saved@example.com" {
		t.Errorf("saved vars = %v, want the saved account's identity", saved)
	}
	if got := descriptorBuiltInProfileSource(d, "", ctx, saved, savedRoot, true); got.RemoteURL != "" || got.LocalPath != "" {
		t.Errorf("profile image = %+v, want none for an account without an avatar", got)
	}
}
