package basic

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"

	"TcNo-Acc-Switcher/internal/platform"
	"TcNo-Acc-Switcher/internal/sqliteread"
)

const latestModifiedFilePrefix = "LATEST_MODIFIED_FILE:"
const sqlitePrefix = "SQLITE:"
const jsonSelectValuePrefix = "JSON_SELECT::"

func isJSONSelectValueReference(v string) bool {
	return strings.HasPrefix(strings.TrimSpace(v), jsonSelectValuePrefix)
}

func resolveJSONSelectValue(v, folder string, ctx platform.PathTokenContext) (string, bool, error) {
	trimmed := strings.TrimSpace(v)
	if !isJSONSelectValueReference(trimmed) {
		return "", false, nil
	}
	filePath, jsonPath, ok := parseJSONSelectPlain("JSON_SELECT", trimmed)
	if !ok || strings.TrimSpace(filePath) == "" || strings.TrimSpace(jsonPath) == "" {
		return "", true, fmt.Errorf("bad JSON_SELECT format")
	}
	expandedPath := expandPlatformPath(filePath, folder, ctx)
	data, err := os.ReadFile(expandedPath)
	if err != nil {
		return "", true, fmt.Errorf("read JSON_SELECT file %q: %w", expandedPath, err)
	}
	return strings.TrimSpace(gjson.GetBytes(data, jsonPath).String()), true, nil
}

func mapSavedJSONSelectReference(d platform.Descriptor, folder string, ctx platform.PathTokenContext, ref, accountCacheRoot string) string {
	filePath, jsonPath, ok := parseJSONSelectPlain("JSON_SELECT", strings.TrimSpace(ref))
	if !ok {
		return ""
	}
	savedPath := mapLivePathToSavedPath(d, folder, ctx, expandPlatformPath(filePath, folder, ctx), accountCacheRoot)
	if strings.TrimSpace(savedPath) == "" {
		return ""
	}
	return jsonSelectValuePrefix + savedPath + "::" + jsonPath
}

func resolveJSONSelectReference(d platform.Descriptor, ref, folder string, ctx platform.PathTokenContext, accountCacheRoot string, saved bool) (string, error) {
	if saved {
		mapped := mapSavedJSONSelectReference(d, folder, ctx, ref, accountCacheRoot)
		if mapped == "" {
			return "", fmt.Errorf("JSON_SELECT file is not one of the saved login files")
		}
		ref = mapped
	}
	resolved, _, err := resolveJSONSelectValue(ref, folder, ctx)
	return resolved, err
}

func resolveLatestModifiedFileValue(v, folder string, ctx platform.PathTokenContext) (string, bool, error) {
	trimmed := strings.TrimSpace(v)
	if !strings.HasPrefix(strings.ToUpper(trimmed), latestModifiedFilePrefix) {
		return "", false, nil
	}
	pattern := strings.TrimSpace(trimmed[len(latestModifiedFilePrefix):])
	if pattern == "" {
		return "", true, fmt.Errorf("empty latest modified file pattern")
	}
	pattern = expandPlatformPath(pattern, folder, ctx)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", true, fmt.Errorf("glob latest modified file %q: %w", pattern, err)
	}
	if len(matches) == 0 {
		return "", true, nil
	}
	latestPath := ""
	var latestModTime int64
	for _, p := range matches {
		st, statErr := os.Stat(p)
		if statErr != nil || st.IsDir() {
			continue
		}
		mt := st.ModTime().UnixNano()
		if latestPath == "" || mt > latestModTime {
			latestPath = p
			latestModTime = mt
		}
	}
	return strings.TrimSpace(latestPath), true, nil
}

func resolveSQLiteValue(v, folder string, ctx platform.PathTokenContext) (string, bool, error) {
	trimmed := strings.TrimSpace(v)
	if !strings.HasPrefix(strings.ToUpper(trimmed), sqlitePrefix) {
		return "", false, nil
	}
	rest := strings.TrimSpace(trimmed[len(sqlitePrefix):])
	pipeIdx := strings.Index(rest, "|")
	if pipeIdx <= 0 {
		return "", true, fmt.Errorf("bad SQLITE format")
	}
	dbPath := strings.TrimSpace(rest[:pipeIdx])
	query := strings.TrimSpace(rest[pipeIdx+1:])
	if dbPath == "" || query == "" {
		return "", true, fmt.Errorf("bad SQLITE format")
	}
	expandedDBPath := expandPlatformPath(dbPath, folder, ctx)
	value, err := sqliteread.QueryScalar(expandedDBPath, query)
	if err != nil {
		return "", true, fmt.Errorf("query SQLITE db %q: %w", expandedDBPath, err)
	}
	return strings.TrimSpace(value), true, nil
}
