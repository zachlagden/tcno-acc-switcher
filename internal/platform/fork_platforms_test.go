package platform

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreEmbeddedForkPlatforms_addsMissingEntry(t *testing.T) {
	t.Parallel()
	base := []byte(`{"Platforms":{"Steam":{"Identifiers":["s"]}},"Platform-WIP":[],"Version":"9.9.9"}`)
	embedded := []byte(`{"Platforms":{"Steam":{"Identifiers":["s"]},"Wispr Flow":{"Identifiers":["wf"]}},"Version":"1.0.0"}`)

	merged, changed, err := restoreEmbeddedForkPlatforms(base, embedded)
	if err != nil {
		t.Fatalf("restoreEmbeddedForkPlatforms: %v", err)
	}
	if !changed {
		t.Fatal("expected the missing Wispr Flow entry to be restored")
	}

	var out struct {
		Platforms   map[string]json.RawMessage `json:"Platforms"`
		PlatformWIP json.RawMessage            `json:"Platform-WIP"`
		Version     string                     `json:"Version"`
	}
	if err := json.Unmarshal(merged, &out); err != nil {
		t.Fatalf("parse merged: %v", err)
	}
	if _, ok := out.Platforms["Wispr Flow"]; !ok {
		t.Error("Wispr Flow missing after restore")
	}
	if _, ok := out.Platforms["Steam"]; !ok {
		t.Error("existing Steam entry dropped")
	}
	if out.Version != "9.9.9" {
		t.Errorf("Version = %q, want the on-disk 9.9.9 kept", out.Version)
	}
	if len(out.PlatformWIP) == 0 {
		t.Error("unrelated top-level key Platform-WIP dropped")
	}
}

func TestRestoreEmbeddedForkPlatforms_replacesStaleEntry(t *testing.T) {
	t.Parallel()
	base := []byte(`{"Platforms":{"Wispr Flow":{"Extras":{"ClosingMethod":"Electron"}}},"Version":"9.9.9"}`)
	embedded := []byte(`{"Platforms":{"Wispr Flow":{"Extras":{"ClosingMethod":"Close","QuitArgs":"--quit-app"}}}}`)

	merged, changed, err := restoreEmbeddedForkPlatforms(base, embedded)
	if err != nil {
		t.Fatalf("restoreEmbeddedForkPlatforms: %v", err)
	}
	if !changed {
		t.Fatal("an older copy of a fork entry must be brought up to date, or descriptor fixes never reach installs")
	}
	var out platformsFile
	if err := json.Unmarshal(merged, &out); err != nil {
		t.Fatalf("parse merged: %v", err)
	}
	if !sameJSON(out.Platforms["Wispr Flow"], json.RawMessage(`{"Extras":{"ClosingMethod":"Close","QuitArgs":"--quit-app"}}`)) {
		t.Errorf("entry = %s, want the embedded one", out.Platforms["Wispr Flow"])
	}
	if out.Version != "9.9.9" {
		t.Errorf("Version = %q, want the on-disk 9.9.9 kept", out.Version)
	}
}

func TestRestoreEmbeddedForkPlatforms_keepsIdenticalEntry(t *testing.T) {
	t.Parallel()
	base := []byte("{\n  \"Platforms\": {\n    \"Wispr Flow\": { \"Identifiers\": [ \"wf\" ] }\n  },\n  \"Version\": \"9.9.9\"\n}")
	embedded := []byte(`{"Platforms":{"Wispr Flow":{"Identifiers":["wf"]}}}`)

	merged, changed, err := restoreEmbeddedForkPlatforms(base, embedded)
	if err != nil {
		t.Fatalf("restoreEmbeddedForkPlatforms: %v", err)
	}
	if changed {
		t.Error("a whitespace-only difference must not rewrite the catalog")
	}
	if !bytes.Equal(merged, base) {
		t.Error("unchanged catalog must be returned as-is")
	}
}

func TestRestoreEmbeddedForkPlatforms_embeddedLacksEntry(t *testing.T) {
	t.Parallel()
	base := []byte(`{"Platforms":{"Steam":{}},"Version":"9.9.9"}`)
	embedded := []byte(`{"Platforms":{"Steam":{}}}`)

	_, changed, err := restoreEmbeddedForkPlatforms(base, embedded)
	if err != nil {
		t.Fatalf("restoreEmbeddedForkPlatforms: %v", err)
	}
	if changed {
		t.Error("nothing to restore when the embedded catalog lacks the entry")
	}
}

func TestRestoreEmbeddedForkPlatforms_rejectsCatalogWithoutPlatforms(t *testing.T) {
	t.Parallel()
	if _, _, err := restoreEmbeddedForkPlatforms([]byte(`{"Version":"1"}`), nil); err == nil {
		t.Error("expected an error for a catalog with no Platforms object")
	}
}

func TestForkRestoredPlatforms_shippedCatalogs(t *testing.T) {
	t.Parallel()
	for _, file := range []string{"Platforms.json", "Platforms.mac.json"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		var catalog platformsFile
		if err := json.Unmarshal(raw, &catalog); err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, name := range forkRestoredPlatforms {
			if _, ok := catalog.Platforms[name]; !ok {
				t.Errorf("%s does not ship %s, so it could never be restored", file, name)
			}
		}
	}
}
