package wisprstats

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrNoSnapshot = errors.New("wisprstats: config has no stats snapshot")

type Breakdown struct {
	TotalWords      int64   `json:"totalWords"`
	WordsPerMinute  float64 `json:"wordsPerMinute"`
	WordsThisWeek   int64   `json:"wordsThisWeek"`
	SpeakingSeconds float64 `json:"speakingSeconds"`
	LastDictationAt string  `json:"lastDictationAt"`
}

type Stats struct {
	TotalWords       int64      `json:"totalWords"`
	WordsPerMinute   float64    `json:"wordsPerMinute"`
	WordsThisWeek    int64      `json:"wordsThisWeek"`
	SpeakingSeconds  float64    `json:"speakingSeconds"`
	RecordingSeconds float64    `json:"recordingSeconds"`
	DayStreak        int        `json:"dayStreak"`
	WeekStreak       int        `json:"weekStreak"`
	AppCount         int        `json:"appCount"`
	LastDictationAt  string     `json:"lastDictationAt"`
	Desktop          *Breakdown `json:"desktop"`
	Mobile           *Breakdown `json:"mobile"`
}

type Totals struct {
	Accounts        int     `json:"accounts"`
	TotalWords      int64   `json:"totalWords"`
	WordsThisWeek   int64   `json:"wordsThisWeek"`
	SpeakingSeconds float64 `json:"speakingSeconds"`
	WordsPerMinute  float64 `json:"wordsPerMinute"`
	BestDayStreak   int     `json:"bestDayStreak"`
	BestWeekStreak  int     `json:"bestWeekStreak"`
	LastDictationAt string  `json:"lastDictationAt"`
}

type snapshotFile struct {
	Prefs struct {
		Cache struct {
			Statistics *struct {
				WeekStreak            int             `json:"weekStreak"`
				DayStreak             int             `json:"dayStreak"`
				TotalWords            int64           `json:"totalWords"`
				TotalDuration         float64         `json:"totalDuration"`
				TotalNonEmptyDuration float64         `json:"totalNonEmptyDuration"`
				AverageWPM            float64         `json:"averageWPM"`
				WordsThisWeek         int64           `json:"wordsThisWeek"`
				LastTranscript        string          `json:"lastTranscriptTimestamp"`
				TotalApps             json.RawMessage `json:"totalApps"`
			} `json:"statistics"`
		} `json:"cache"`
	} `json:"prefs"`
}

func ParseSnapshot(data []byte, now time.Time) (*Stats, error) {
	var f snapshotFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("wisprstats: parse config: %w", err)
	}
	s := f.Prefs.Cache.Statistics
	if s == nil || s.TotalDuration < 0 {
		return nil, ErrNoSnapshot
	}
	last := normalizeTimestamp(s.LastTranscript)
	out := &Stats{
		TotalWords:       s.TotalWords,
		WordsPerMinute:   s.AverageWPM,
		WordsThisWeek:    s.WordsThisWeek,
		SpeakingSeconds:  s.TotalNonEmptyDuration,
		RecordingSeconds: s.TotalDuration,
		DayStreak:        s.DayStreak,
		WeekStreak:       s.WeekStreak,
		AppCount:         countArray(s.TotalApps),
		LastDictationAt:  last,
	}
	ageSnapshot(out, now)
	return out, nil
}

func ageSnapshot(s *Stats, now time.Time) {
	last, ok := parseTimestamp(s.LastDictationAt)
	if !ok || last.IsZero() {
		return
	}
	last = last.In(now.Location())
	today := startOfDay(now)
	if last.Before(today.AddDate(0, 0, -1)) {
		s.DayStreak = 0
	}
	thisWeek := startOfWeek(now)
	if last.Before(thisWeek) {
		s.WordsThisWeek = 0
	}
	if last.Before(thisWeek.AddDate(0, 0, -7)) {
		s.WeekStreak = 0
	}
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func startOfWeek(t time.Time) time.Time {
	day := startOfDay(t)
	return day.AddDate(0, 0, -int(day.Weekday()))
}

type apiStats struct {
	IsInitialized         bool            `json:"is_initialized"`
	TotalWords            int64           `json:"total_words"`
	TotalDuration         float64         `json:"total_duration"`
	TotalNonEmptyDuration float64         `json:"total_non_empty_duration"`
	WordsPerMinute        float64         `json:"words_per_minute"`
	DayStreak             int             `json:"day_streak"`
	WeekStreak            int             `json:"week_streak"`
	WordsThisWeek         int64           `json:"words_this_week"`
	LastTranscript        string          `json:"last_transcript_timestamp"`
	TotalApps             json.RawMessage `json:"total_apps"`

	DesktopTotalWords       *int64   `json:"desktop_total_words"`
	DesktopWordsPerMinute   float64  `json:"desktop_words_per_minute"`
	DesktopWordsThisWeek    int64    `json:"desktop_words_this_week"`
	DesktopNonEmptyDuration float64  `json:"desktop_total_non_empty_duration"`
	DesktopLastTranscript   string   `json:"desktop_last_transcript_timestamp"`
	MobileTotalWords        *int64   `json:"mobile_total_words"`
	MobileWordsPerMinute    float64  `json:"mobile_words_per_minute"`
	MobileWordsThisWeek     int64    `json:"mobile_words_this_week"`
	MobileNonEmptyDuration  float64  `json:"mobile_total_non_empty_duration"`
	MobileLastTranscript    string   `json:"mobile_last_transcript_timestamp"`
	FirstWeeklyLimitHitAt   *float64 `json:"first_weekly_limit_hit_at"`
}

var ErrNotInitialized = errors.New("wisprstats: Wispr has not built stats for this account yet")

func ParseAPIStats(data []byte) (*Stats, error) {
	var a apiStats
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("wisprstats: parse stats response: %w", err)
	}
	if !a.IsInitialized {
		return nil, ErrNotInitialized
	}
	out := &Stats{
		TotalWords:       a.TotalWords,
		WordsPerMinute:   a.WordsPerMinute,
		WordsThisWeek:    a.WordsThisWeek,
		SpeakingSeconds:  a.TotalNonEmptyDuration,
		RecordingSeconds: a.TotalDuration,
		DayStreak:        a.DayStreak,
		WeekStreak:       a.WeekStreak,
		AppCount:         countArray(a.TotalApps),
		LastDictationAt:  normalizeTimestamp(a.LastTranscript),
	}
	if a.DesktopTotalWords != nil {
		out.Desktop = &Breakdown{
			TotalWords:      *a.DesktopTotalWords,
			WordsPerMinute:  a.DesktopWordsPerMinute,
			WordsThisWeek:   a.DesktopWordsThisWeek,
			SpeakingSeconds: a.DesktopNonEmptyDuration,
			LastDictationAt: normalizeTimestamp(a.DesktopLastTranscript),
		}
	}
	if a.MobileTotalWords != nil {
		out.Mobile = &Breakdown{
			TotalWords:      *a.MobileTotalWords,
			WordsPerMinute:  a.MobileWordsPerMinute,
			WordsThisWeek:   a.MobileWordsThisWeek,
			SpeakingSeconds: a.MobileNonEmptyDuration,
			LastDictationAt: normalizeTimestamp(a.MobileLastTranscript),
		}
	}
	return out, nil
}

func Summarize(rows []AccountReport) Totals {
	var t Totals
	var weighted float64
	var lastAt time.Time
	for _, row := range rows {
		s := row.Stats
		if s == nil {
			continue
		}
		t.Accounts++
		t.TotalWords += s.TotalWords
		t.WordsThisWeek += s.WordsThisWeek
		t.SpeakingSeconds += s.SpeakingSeconds
		weighted += s.WordsPerMinute * s.SpeakingSeconds
		if s.DayStreak > t.BestDayStreak {
			t.BestDayStreak = s.DayStreak
		}
		if s.WeekStreak > t.BestWeekStreak {
			t.BestWeekStreak = s.WeekStreak
		}
		if at, ok := parseTimestamp(s.LastDictationAt); ok && at.After(lastAt) {
			lastAt = at
			t.LastDictationAt = s.LastDictationAt
		}
	}
	if t.SpeakingSeconds > 0 {
		t.WordsPerMinute = weighted / t.SpeakingSeconds
	}
	return t
}

func countArray(raw json.RawMessage) int {
	var items []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return 0
	}
	return len(items)
}

var timestampLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999 -07:00",
}

func parseTimestamp(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range timestampLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func normalizeTimestamp(s string) string {
	t, ok := parseTimestamp(s)
	if !ok || t.Unix() <= 0 {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
