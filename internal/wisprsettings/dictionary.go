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
	Upserts []RemoteItem
	Added   int
	Updated int
}

func PlanDictionary(remote []RemoteItem, entries []DictionaryEntry, now time.Time, newID func() string) (DictionaryPlan, error) {
	byWord := map[string]RemoteItem{}
	for _, item := range remote {
		if !isPersonal(item) {
			continue
		}
		word := strings.TrimSpace(itemString(item, "word"))
		if word == "" {
			continue
		}
		if existing, ok := byWord[word]; ok && !itemBool(existing, "is_deleted") {
			continue
		}
		byWord[word] = item
	}

	stamp, err := json.Marshal(now.UTC().Format("2006-01-02T15:04:05.000Z"))
	if err != nil {
		return DictionaryPlan{}, err
	}
	plan := DictionaryPlan{}
	for _, e := range entries {
		word := strings.TrimSpace(e.Word)
		if word == "" {
			continue
		}
		existing, ok := byWord[word]
		if !ok {
			item, err := newDictionaryItem(e, word, newID(), stamp)
			if err != nil {
				return DictionaryPlan{}, err
			}
			plan.Upserts = append(plan.Upserts, item)
			byWord[word] = item
			plan.Added++
			continue
		}
		if !itemBool(existing, "is_deleted") && sameEntry(existing, e) {
			continue
		}
		item := maps.Clone(existing)
		if err := setEntryFields(item, e); err != nil {
			return DictionaryPlan{}, err
		}
		item["is_deleted"] = json.RawMessage("false")
		item["modified_at"] = stamp
		plan.Upserts = append(plan.Upserts, item)
		byWord[word] = item
		plan.Updated++
	}
	return plan, nil
}

func ImportableEntries(remote []RemoteItem, email string) ([]DictionaryEntry, int) {
	out := []DictionaryEntry{}
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

func sameEntry(item RemoteItem, e DictionaryEntry) bool {
	return itemString(item, "replacement") == e.Replacement &&
		itemString(item, "replacement_html") == e.ReplacementHTML &&
		itemBool(item, "is_snippet") == e.IsSnippet
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
