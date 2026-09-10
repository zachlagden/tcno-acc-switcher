package updatecheck

import (
	"strings"
	"testing"
)

func TestParseLatestRelease(t *testing.T) {
	t.Parallel()
	version, message, err := parseLatestRelease([]byte(`{"tag_name":"v4.1.0","draft":false,"assets":[{"name":"TcNo-Acc-Switcher.exe"}]}`))
	if err != nil {
		t.Fatalf("parseLatestRelease: %v", err)
	}
	if version != "4.1.0" {
		t.Errorf("version = %q, want 4.1.0 without the v prefix", version)
	}
	if !strings.Contains(message, "4.1.0") {
		t.Errorf("message %q should name the version", message)
	}
}

func TestParseLatestRelease_rejectsUnusable(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"draft":   `{"tag_name":"v4.1.0","draft":true}`,
		"no tag":  `{"tag_name":""}`,
		"garbage": `not json`,
	} {
		if _, _, err := parseLatestRelease([]byte(body)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestReleaseURLsPointAtTheFork(t *testing.T) {
	t.Parallel()
	for _, url := range []string{ReleasePageURL, LatestReleaseAPIURL, ReleaseAssetURL("Platforms.json"), PlatformsJSONRawURL("")} {
		if !strings.Contains(url, ReleaseRepository) {
			t.Errorf("%q does not point at %s", url, ReleaseRepository)
		}
		if strings.Contains(strings.ToLower(url), "tcnoco") {
			t.Errorf("%q still points at upstream", url)
		}
	}
	if got := PlatformsJSONRawURL("Platforms.mac.json"); got != "https://github.com/"+ReleaseRepository+"/releases/latest/download/Platforms.mac.json" {
		t.Errorf("PlatformsJSONRawURL = %q, want the latest release asset", got)
	}
}
