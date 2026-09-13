package sqliteread

import "fmt"

func ReadRows(path, table string, columns []string) ([][]*string, error) {
	r, err := open(path)
	if err != nil {
		return nil, err
	}
	defer r.close()

	tbl, err := r.schema(table)
	if err != nil {
		return nil, err
	}
	idx := make([]int, len(columns))
	for i, c := range columns {
		idx[i] = tbl.index(c)
		if idx[i] < 0 {
			return nil, fmt.Errorf("table %q has no column %q", table, c)
		}
	}

	var out [][]*string
	err = r.walk(tbl.root, func(rowid int64, rec []byte) (bool, error) {
		vals, err := decodeRecord(rec)
		if err != nil {
			return false, err
		}
		row := make([]*string, len(idx))
		for i, col := range idx {
			v := tbl.value(vals, col, rowid)
			if v.kind == kindNull {
				continue
			}
			text := asText(v)
			row[i] = &text
		}
		out = append(out, row)
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
