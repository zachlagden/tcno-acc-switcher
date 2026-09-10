package platform

import (
	"bytes"
	"encoding/json"
	"errors"
)

func sameJSON(a, b json.RawMessage) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

var forkRestoredPlatforms = []string{"Wispr Flow"}

func restoreEmbeddedForkPlatforms(base, embedded []byte) ([]byte, bool, error) {
	var current map[string]json.RawMessage
	if err := json.Unmarshal(base, &current); err != nil {
		return nil, false, err
	}
	var currentPlatforms map[string]json.RawMessage
	if err := json.Unmarshal(current["Platforms"], &currentPlatforms); err != nil || currentPlatforms == nil {
		return nil, false, errors.New("Platforms.json missing Platforms")
	}

	var defaults platformsFile
	if len(embedded) > 0 {
		if err := json.Unmarshal(embedded, &defaults); err != nil {
			return nil, false, err
		}
	}

	changed := false
	for _, name := range forkRestoredPlatforms {
		entry, ok := defaults.Platforms[name]
		if !ok {
			continue
		}
		if existing, ok := currentPlatforms[name]; ok && sameJSON(existing, entry) {
			continue
		}
		currentPlatforms[name] = entry
		changed = true
	}
	if !changed {
		return base, false, nil
	}

	platformsRaw, err := json.Marshal(currentPlatforms)
	if err != nil {
		return nil, false, err
	}
	current["Platforms"] = platformsRaw
	merged, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return merged, true, nil
}
