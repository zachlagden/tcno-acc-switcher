package wisprsettings

import (
	"encoding/json"
	"maps"
	"strings"
	"time"

	"TcNo-Acc-Switcher/internal/sqliteread"
)

const (
	PersonalTeamID = "00000000-0000-0000-0000-000000000000"

	sourceDefault = "default"
	sourceManual  = "manual"
)

type DictionaryPlan struct {
	Upserts  []RemoteItem
	Added    int
	Restored int
}

func NormalizeWord(word string) string {
	return strings.ToLower(strings.Join(strings.Fields(word), " "))
}

func PlanDictionary(remote, local []RemoteItem, entries []DictionaryEntry, now time.Time, newID func() string) (DictionaryPlan, error) {
	present := map[string]bool{}
	for _, items := range [][]RemoteItem{remote, local} {
		for _, item := range items {
			if key := NormalizeWord(itemString(item, "word")); key != "" && !itemBool(item, "is_deleted") {
				present[key] = true
			}
		}
	}
	deleted := map[string]RemoteItem{}
	for _, item := range remote {
		key := NormalizeWord(itemString(item, "word"))
		if key == "" || !itemBool(item, "is_deleted") || !isPersonal(item) {
			continue
		}
		if existing, ok := deleted[key]; ok && itemString(existing, "modified_at") >= itemString(item, "modified_at") {
			continue
		}
		deleted[key] = item
	}

	stamp, err := json.Marshal(now.UTC().Format("2006-01-02T15:04:05.000Z"))
	if err != nil {
		return DictionaryPlan{}, err
	}
	plan := DictionaryPlan{}
	for _, e := range entries {
		key := NormalizeWord(e.Word)
		if key == "" || present[key] {
			continue
		}
		present[key] = true
		if tombstone, ok := deleted[key]; ok {
			item := maps.Clone(tombstone)
			item["is_deleted"] = json.RawMessage("false")
			item["modified_at"] = stamp
			plan.Upserts = append(plan.Upserts, item)
			plan.Restored++
			continue
		}
		item, err := newDictionaryItem(e, strings.Join(strings.Fields(e.Word), " "), newID(), stamp)
		if err != nil {
			return DictionaryPlan{}, err
		}
		plan.Upserts = append(plan.Upserts, item)
		plan.Added++
	}
	return plan, nil
}

func ImportableEntries(remote []RemoteItem, email string) ([]DictionaryEntry, int) {
	out := []DictionaryEntry{}
	seen := map[string]bool{}
	excluded := 0
	for _, item := range remote {
		if itemBool(item, "is_deleted") || !isPersonal(item) {
			continue
		}
		e := DictionaryEntry{
			Word:            strings.TrimSpace(itemString(item, "word")),
			Replacement:     itemString(item, "replacement"),
			ReplacementHTML: itemString(item, "replacement_html"),
			IsSnippet:       itemBool(item, "is_snippet"),
		}
		if e.Word == "" {
			continue
		}
		if itemString(item, "source") == sourceDefault || mentionsEmail(e, email) {
			excluded++
			continue
		}
		key := NormalizeWord(e.Word)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out, excluded
}

func ReadLocalDictionary(dbPath string) ([]RemoteItem, error) {
	columns := []string{"phrase", "replacement", "replacementHtml", "teamDictionaryId", "isDeleted", "isSnippet", "source"}
	rows, err := sqliteread.ReadRows(dbPath, "Dictionary", columns)
	if err != nil {
		return nil, err
	}
	out := make([]RemoteItem, 0, len(rows))
	for _, row := range rows {
		item := RemoteItem{}
		setRawString(item, "word", row[0])
		setRawString(item, "replacement", row[1])
		setRawString(item, "replacement_html", row[2])
		setRawString(item, "team_dictionary_id", row[3])
		item["is_deleted"] = rawBool(row[4])
		item["is_snippet"] = rawBool(row[5])
		setRawString(item, "source", row[6])
		out = append(out, item)
	}
	return out, nil
}

func newDictionaryItem(e DictionaryEntry, word, id string, stamp json.RawMessage) (RemoteItem, error) {
	item := RemoteItem{
		"team_dictionary_id": json.RawMessage(`"` + PersonalTeamID + `"`),
		"is_manual":          json.RawMessage("true"),
		"created_at":         stamp,
		"modified_at":        stamp,
		"is_deleted":         json.RawMessage("false"),
		"frequency_used":     json.RawMessage("0"),
		"last_used":          json.RawMessage("null"),
		"source":             json.RawMessage(`"` + sourceManual + `"`),
		"observed_source":    json.RawMessage("null"),
	}
	for key, value := range map[string]string{"id": id, "word": word} {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		item[key] = encoded
	}
	if err := setEntryFields(item, e); err != nil {
		return nil, err
	}
	return item, nil
}

func setEntryFields(item RemoteItem, e DictionaryEntry) error {
	for key, value := range map[string]string{"replacement": e.Replacement, "replacement_html": e.ReplacementHTML} {
		encoded, err := nullableString(value)
		if err != nil {
			return err
		}
		item[key] = encoded
	}
	item["is_snippet"] = json.RawMessage("false")
	if e.IsSnippet {
		item["is_snippet"] = json.RawMessage("true")
	}
	return nil
}

func mentionsEmail(e DictionaryEntry, email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	for _, s := range []string{e.Word, e.Replacement, e.ReplacementHTML} {
		if strings.Contains(strings.ToLower(s), email) {
			return true
		}
	}
	return false
}

func isPersonal(item RemoteItem) bool {
	team := itemString(item, "team_dictionary_id")
	return team == "" || team == PersonalTeamID
}

func itemString(item RemoteItem, key string) string {
	var s string
	if err := json.Unmarshal(item[key], &s); err != nil {
		return ""
	}
	return s
}

func itemBool(item RemoteItem, key string) bool {
	var b bool
	if err := json.Unmarshal(item[key], &b); err == nil {
		return b
	}
	var n float64
	if err := json.Unmarshal(item[key], &n); err == nil {
		return n != 0
	}
	return false
}

func nullableString(s string) (json.RawMessage, error) {
	if s == "" {
		return json.RawMessage("null"), nil
	}
	return json.Marshal(s)
}

func setRawString(item RemoteItem, key string, value *string) {
	if value == nil {
		item[key] = json.RawMessage("null")
		return
	}
	encoded, err := json.Marshal(*value)
	if err != nil {
		item[key] = json.RawMessage("null")
		return
	}
	item[key] = encoded
}

func rawBool(value *string) json.RawMessage {
	if value != nil && *value != "" && *value != "0" {
		return json.RawMessage("true")
	}
	return json.RawMessage("false")
}
