package wisprsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	ProfileFileName = "WisprFlowSyncSettings.json"
	profileVersion  = 1

	TargetAll      = "all"
	TargetSelected = "selected"

	maxDictionaryEntries = 5000
	maxWordRunes         = 255
	maxReplacementRunes  = 10000
	maxVoices            = 64
	maxAppNames          = 256
)

var (
	StyleValues       = []string{"formal", "casual", "veryCasual", "excited", "genz", "default"}
	CleanupLevels     = []string{"none", "light", "medium", "high", "full"}
	StyleCategories   = []string{"work", "email", "personal", "other"}
	voiceSourceValues = []string{"builtIn", "custom"}
)

type Parts struct {
	Styles      bool `json:"styles"`
	AutoCleanup bool `json:"autoCleanup"`
	Voices      bool `json:"voices"`
	Dictionary  bool `json:"dictionary"`
}

type Styles struct {
	Work     string `json:"work"`
	Email    string `json:"email"`
	Personal string `json:"personal"`
	Other    string `json:"other"`
}

type Voice struct {
	ID               string   `json:"id"`
	Source           string   `json:"source"`
	Name             string   `json:"name"`
	AppNames         []string `json:"appNames"`
	AutoCleanupLevel string   `json:"autoCleanupLevel"`
	StylePreference  string   `json:"stylePreference"`
}

type DictionaryEntry struct {
	Word            string `json:"word"`
	Replacement     string `json:"replacement"`
	ReplacementHTML string `json:"replacementHtml"`
	IsSnippet       bool   `json:"isSnippet"`
}

type ImportSource struct {
	DisplayName string `json:"displayName"`
	At          string `json:"at"`
}

type Profile struct {
	Version          int               `json:"version"`
	Parts            Parts             `json:"parts"`
	Styles           Styles            `json:"styles"`
	AutoCleanupLevel string            `json:"autoCleanupLevel"`
	UserVoices       map[string]Voice  `json:"userVoices"`
	Dictionary       []DictionaryEntry `json:"dictionary"`
	ApplyOnSwitch    bool              `json:"applyOnSwitch"`
	Target           string            `json:"target"`
	SelectedAccounts []string          `json:"selectedAccounts"`
	ImportedFrom     *ImportSource     `json:"importedFrom"`
}

func DefaultProfile() Profile {
	return Profile{
		Version:          profileVersion,
		Target:           TargetAll,
		UserVoices:       map[string]Voice{},
		Dictionary:       []DictionaryEntry{},
		SelectedAccounts: []string{},
	}
}

func (s Styles) ByCategory() map[string]string {
	return map[string]string{
		"work":     s.Work,
		"email":    s.Email,
		"personal": s.Personal,
		"other":    s.Other,
	}
}

func StylesFromMap(m map[string]string) Styles {
	return Styles{
		Work:     validOrEmpty(m["work"], StyleValues),
		Email:    validOrEmpty(m["email"], StyleValues),
		Personal: validOrEmpty(m["personal"], StyleValues),
		Other:    validOrEmpty(m["other"], StyleValues),
	}
}

func (p Profile) Targets(uniqueID string) bool {
	uniqueID = strings.TrimSpace(uniqueID)
	if uniqueID == "" {
		return false
	}
	if p.Target != TargetSelected {
		return true
	}
	return slices.Contains(p.SelectedAccounts, uniqueID)
}

func (p Profile) HasWork() bool {
	return p.Parts.Styles || p.Parts.AutoCleanup || p.Parts.Voices || p.Parts.Dictionary
}

func (p Profile) AppliesOnSwitchTo(uniqueID string, offline bool) bool {
	return !offline && p.ApplyOnSwitch && p.HasWork() && p.Targets(uniqueID)
}

func (p Profile) Normalize() (Profile, error) {
	out := p
	out.Version = profileVersion
	if out.Target == "" {
		out.Target = TargetAll
	}
	if out.Target != TargetAll && out.Target != TargetSelected {
		return Profile{}, fmt.Errorf("unknown target %q", out.Target)
	}
	for category, value := range out.Styles.ByCategory() {
		if value != "" && !slices.Contains(StyleValues, value) {
			return Profile{}, fmt.Errorf("unknown %s style %q", category, value)
		}
	}
	if out.AutoCleanupLevel != "" && !slices.Contains(CleanupLevels, out.AutoCleanupLevel) {
		return Profile{}, fmt.Errorf("unknown auto cleanup level %q", out.AutoCleanupLevel)
	}
	if out.Parts.AutoCleanup && out.AutoCleanupLevel == "" {
		return Profile{}, errors.New("choose an auto cleanup level")
	}

	voices, err := normalizeVoices(out.UserVoices)
	if err != nil {
		return Profile{}, err
	}
	out.UserVoices = voices
	if out.Parts.Voices && len(out.UserVoices) == 0 {
		return Profile{}, errors.New("import voices from an account first")
	}

	entries, err := normalizeDictionary(out.Dictionary)
	if err != nil {
		return Profile{}, err
	}
	out.Dictionary = entries

	selected := make([]string, 0, len(out.SelectedAccounts))
	for _, id := range out.SelectedAccounts {
		id = strings.TrimSpace(id)
		if id != "" && !slices.Contains(selected, id) {
			selected = append(selected, id)
		}
	}
	out.SelectedAccounts = selected

	if out.ImportedFrom != nil {
		name := strings.TrimSpace(out.ImportedFrom.DisplayName)
		if name == "" {
			out.ImportedFrom = nil
		} else {
			out.ImportedFrom = &ImportSource{DisplayName: name, At: strings.TrimSpace(out.ImportedFrom.At)}
		}
	}
	return out, nil
}

func normalizeVoices(in map[string]Voice) (map[string]Voice, error) {
	out := make(map[string]Voice, len(in))
	if len(in) > maxVoices {
		return nil, fmt.Errorf("too many voices (%d)", len(in))
	}
	for key, v := range in {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, errors.New("voice without an id")
		}
		if v.ID == "" {
			v.ID = key
		}
		if v.Source == "" {
			v.Source = "builtIn"
		}
		if !slices.Contains(voiceSourceValues, v.Source) {
			return nil, fmt.Errorf("voice %s has unknown source %q", key, v.Source)
		}
		if v.StylePreference != "" && !slices.Contains(StyleValues, v.StylePreference) {
			return nil, fmt.Errorf("voice %s has unknown style %q", key, v.StylePreference)
		}
		if v.AutoCleanupLevel != "" && !slices.Contains(CleanupLevels, v.AutoCleanupLevel) {
			return nil, fmt.Errorf("voice %s has unknown cleanup level %q", key, v.AutoCleanupLevel)
		}
		if len(v.AppNames) > maxAppNames {
			return nil, fmt.Errorf("voice %s lists too many apps", key)
		}
		if v.AppNames == nil {
			v.AppNames = []string{}
		}
		out[key] = v
	}
	return out, nil
}

func normalizeDictionary(in []DictionaryEntry) ([]DictionaryEntry, error) {
	if len(in) > maxDictionaryEntries {
		return nil, fmt.Errorf("too many dictionary entries (%d)", len(in))
	}
	out := make([]DictionaryEntry, 0, len(in))
	seen := make(map[string]int, len(in))
	for _, e := range in {
		e.Word = strings.TrimSpace(e.Word)
		if e.Word == "" {
			continue
		}
		if utf8.RuneCountInString(e.Word) > maxWordRunes {
			return nil, fmt.Errorf("dictionary word %q is too long", truncateRunes(e.Word, 40))
		}
		if utf8.RuneCountInString(e.Replacement) > maxReplacementRunes || utf8.RuneCountInString(e.ReplacementHTML) > maxReplacementRunes {
			return nil, fmt.Errorf("replacement for %q is too long", truncateRunes(e.Word, 40))
		}
		if e.IsSnippet && strings.TrimSpace(e.Replacement) == "" {
			return nil, fmt.Errorf("snippet %q needs replacement text", truncateRunes(e.Word, 40))
		}
		if i, ok := seen[e.Word]; ok {
			out[i] = e
			continue
		}
		seen[e.Word] = len(out)
		out = append(out, e)
	}
	return out, nil
}

func LoadProfile(path string) (Profile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultProfile(), nil
	}
	if err != nil {
		return Profile{}, err
	}
	p := DefaultProfile()
	if err := json.Unmarshal(data, &p); err != nil {
		return Profile{}, fmt.Errorf("wisprsettings: parse %s: %w", ProfileFileName, err)
	}
	return p.Normalize()
}

func SaveProfile(path string, p Profile, write func(path string, data []byte) error) (Profile, error) {
	normalized, err := p.Normalize()
	if err != nil {
		return Profile{}, err
	}
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return Profile{}, err
	}
	if err := write(path, data); err != nil {
		return Profile{}, err
	}
	return normalized, nil
}

func validOrEmpty(value string, allowed []string) string {
	if slices.Contains(allowed, value) {
		return value
	}
	return ""
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "..."
}
