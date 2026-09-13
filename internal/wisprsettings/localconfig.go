package wisprsettings

import (
	"encoding/json"
	"errors"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	cfgStyles         = "prefs.user.personalizationStyles"
	cfgCleanupLevel   = "prefs.user.autoCleanup.level"
	cfgVoices         = "prefs.user.userVoices"
	cfgVoicesMigrated = "prefs.user.userVoicesMigrated"
	cfgSyncedAt       = "prefs.cache.syncedPrefsModifiedAt"
	cfgPrefsDirty     = "prefs.cache.prefsDirty"
)

var ErrBadConfig = errors.New("wisprsettings: config.json is not valid JSON")

type LocalPrefs struct {
	Styles       Styles
	CleanupLevel string
	Voices       map[string]Voice
}

func ReadLocalPrefs(data []byte) (LocalPrefs, error) {
	if !gjson.ValidBytes(data) {
		return LocalPrefs{}, ErrBadConfig
	}
	out := LocalPrefs{}
	styles := map[string]string{}
	if raw := gjson.GetBytes(data, cfgStyles); raw.IsObject() {
		_ = json.Unmarshal([]byte(raw.Raw), &styles)
	}
	out.Styles = StylesFromMap(styles)
	out.CleanupLevel = validOrEmpty(gjson.GetBytes(data, cfgCleanupLevel).String(), CleanupLevels)
	if raw := gjson.GetBytes(data, cfgVoices); raw.IsObject() {
		voices := map[string]Voice{}
		if err := json.Unmarshal([]byte(raw.Raw), &voices); err == nil {
			if normalized, err := normalizeVoices(voices); err == nil {
				out.Voices = normalized
			}
		}
	}
	return out, nil
}

func SetCachedPrefs(data []byte, merge PrefsMerge, modifiedAt string) ([]byte, error) {
	if !gjson.ValidBytes(data) {
		return nil, ErrBadConfig
	}
	var err error
	if len(merge.Styles) > 0 {
		if data, err = sjson.SetRawBytes(data, cfgStyles, merge.Styles); err != nil {
			return nil, err
		}
	}
	if merge.CleanupLevel != "" {
		if data, err = sjson.SetBytes(data, cfgCleanupLevel, merge.CleanupLevel); err != nil {
			return nil, err
		}
	}
	if modifiedAt != "" {
		if data, err = sjson.SetBytes(data, cfgSyncedAt, modifiedAt); err != nil {
			return nil, err
		}
	}
	return sjson.SetBytes(data, cfgPrefsDirty, false)
}

func SetVoices(data []byte, voices map[string]Voice) ([]byte, error) {
	if !gjson.ValidBytes(data) {
		return nil, ErrBadConfig
	}
	encoded, err := json.Marshal(voices)
	if err != nil {
		return nil, err
	}
	if data, err = sjson.SetRawBytes(data, cfgVoices, encoded); err != nil {
		return nil, err
	}
	return sjson.SetBytes(data, cfgVoicesMigrated, true)
}

func VoicesMatch(data []byte, voices map[string]Voice) bool {
	if !gjson.GetBytes(data, cfgVoicesMigrated).Bool() {
		return false
	}
	raw := gjson.GetBytes(data, cfgVoices)
	if !raw.IsObject() {
		return false
	}
	have := map[string]Voice{}
	if err := json.Unmarshal([]byte(raw.Raw), &have); err != nil {
		return false
	}
	a, errA := json.Marshal(have)
	b, errB := json.Marshal(voices)
	return errA == nil && errB == nil && string(a) == string(b)
}
