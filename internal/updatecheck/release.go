package updatecheck

import (
	"encoding/json"
	"fmt"
	"strings"
)

const ReleaseRepository = "zachlagden/tcno-acc-switcher"

const (
	ReleasePageURL      = "https://github.com/" + ReleaseRepository + "/releases/latest"
	LatestReleaseAPIURL = "https://api.github.com/repos/" + ReleaseRepository + "/releases/latest"
	releaseDownloadBase = "https://github.com/" + ReleaseRepository + "/releases/latest/download/"
)

func ReleaseAssetURL(name string) string {
	return releaseDownloadBase + strings.TrimSpace(name)
}

type latestRelease struct {
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
}

func parseLatestRelease(body []byte) (version string, message string, err error) {
	var r latestRelease
	if err := json.Unmarshal(body, &r); err != nil {
		return "", "", fmt.Errorf("updatecheck: parse latest release: %w", err)
	}
	version = strings.TrimPrefix(strings.TrimSpace(r.TagName), "v")
	if version == "" || r.Draft {
		return "", "", fmt.Errorf("updatecheck: latest release has no published version tag")
	}
	return version, fmt.Sprintf("TcNo Account Switcher %s is available.", version), nil
}
