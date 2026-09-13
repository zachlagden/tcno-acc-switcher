package sqliteread

import "testing"

func TestReadRows(t *testing.T) {
	rows, err := ReadRows(fixture, "other", []string{"k", "v"})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	found := map[string]string{}
	for _, row := range rows {
		if row[0] == nil {
			t.Fatalf("key column read as NULL: %v", row)
		}
		if row[1] != nil {
			found[*row[0]] = *row[1]
		}
	}
	if found["pi"] != "3.5" {
		t.Fatalf("pi = %q, want 3.5 (rows %d)", found["pi"], len(rows))
	}
}

func TestReadRowsNullAndMissingColumn(t *testing.T) {
	rows, err := ReadRows(fixture, "login_cache", []string{"account_id_lo", "battle_tag"})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	sawNull := false
	for _, row := range rows {
		if row[0] != nil && *row[0] == "4242" {
			sawNull = row[1] == nil
		}
	}
	if !sawNull {
		t.Fatal("NULL battle_tag did not read as nil")
	}
	if _, err := ReadRows(fixture, "login_cache", []string{"nope"}); err == nil {
		t.Fatal("expected an error for a missing column")
	}
}
