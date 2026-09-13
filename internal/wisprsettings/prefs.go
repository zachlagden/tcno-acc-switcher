package wisprsettings

import (
	"encoding/json"
	"maps"
)

const (
	prefStylesKey  = "personalization_styles"
	prefCleanupKey = "auto_cleanup_level"
)

type PrefsMerge struct {
	Body         []byte
	Changed      bool
	Styles       json.RawMessage
	CleanupLevel string
}

func MergePrefs(server ServerPrefs, p Profile) (PrefsMerge, error) {
	prefs := maps.Clone(server.Preferences)
	if prefs == nil {
		prefs = map[string]json.RawMessage{}
	}
	out := PrefsMerge{}

	if p.Parts.Styles {
		styles := map[string]json.RawMessage{}
		if raw, ok := prefs[prefStylesKey]; ok && len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &styles); err != nil {
				styles = map[string]json.RawMessage{}
			}
		}
		for _, category := range StyleCategories {
			want := p.Styles.ByCategory()[category]
			if want == "" {
				continue
			}
			var have string
			_ = json.Unmarshal(styles[category], &have)
			if have == want {
				continue
			}
			encoded, err := json.Marshal(want)
			if err != nil {
				return PrefsMerge{}, err
			}
			styles[category] = encoded
			out.Changed = true
		}
		encoded, err := json.Marshal(styles)
		if err != nil {
			return PrefsMerge{}, err
		}
		prefs[prefStylesKey] = encoded
		out.Styles = encoded
	}

	if p.Parts.AutoCleanup && p.AutoCleanupLevel != "" {
		var have string
		_ = json.Unmarshal(prefs[prefCleanupKey], &have)
		if have != p.AutoCleanupLevel {
			encoded, err := json.Marshal(p.AutoCleanupLevel)
			if err != nil {
				return PrefsMerge{}, err
			}
			prefs[prefCleanupKey] = encoded
			out.Changed = true
		}
		out.CleanupLevel = p.AutoCleanupLevel
	}

	body, err := json.Marshal(struct {
		Preferences map[string]json.RawMessage `json:"preferences"`
		ModifiedAt  string                     `json:"modified_at"`
	}{prefs, server.ModifiedAt})
	if err != nil {
		return PrefsMerge{}, err
	}
	out.Body = body
	return out, nil
}
