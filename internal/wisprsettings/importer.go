package wisprsettings

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"TcNo-Acc-Switcher/internal/wisprstats"
)

const (
	SourceServer = "server"
	SourceLocal  = "local"
	SourceNone   = "none"
)

type ImportResult struct {
	Profile          Profile `json:"profile"`
	PrefsSource      string  `json:"prefsSource"`
	DictionarySource string  `json:"dictionarySource"`
	VoicesFound      bool    `json:"voicesFound"`
	Excluded         int     `json:"excluded"`
	Problem          string  `json:"problem"`
	TokenRefreshed   bool    `json:"tokenRefreshed"`
}

func (s *Syncer) Import(ctx context.Context, a wisprstats.Account, network, allowRefresh bool) ImportResult {
	p := DefaultProfile()
	p.ImportedFrom = &ImportSource{DisplayName: a.DisplayName, At: s.now().UTC().Format(time.RFC3339)}
	res := ImportResult{PrefsSource: SourceNone, DictionarySource: SourceNone}

	var local LocalPrefs
	haveLocal := false
	if data, err := os.ReadFile(filepath.Join(a.Dir, wisprstats.ConfigFileName)); err == nil {
		if parsed, err := ReadLocalPrefs(data); err == nil {
			local = parsed
			haveLocal = true
		}
	}
	if len(local.Voices) > 0 {
		p.UserVoices = local.Voices
		res.VoicesFound = true
	}

	auth := &accountAuth{s: s, acct: a, allowRefresh: allowRefresh}
	email := ""
	if err := auth.load(); err == nil {
		email = auth.email
	}

	if network {
		err := auth.do(ctx, func(token string) error {
			server, err := s.Client.GetPrefs(ctx, token)
			if err != nil {
				return err
			}
			styles := map[string]string{}
			_ = json.Unmarshal(server.Preferences[prefStylesKey], &styles)
			p.Styles = StylesFromMap(styles)
			var level string
			_ = json.Unmarshal(server.Preferences[prefCleanupKey], &level)
			p.AutoCleanupLevel = validOrEmpty(level, CleanupLevels)
			res.PrefsSource = SourceServer
			return nil
		})
		if err != nil {
			res.Problem = problemFor(err)
		}
	}
	if res.PrefsSource == SourceNone && haveLocal {
		p.Styles = local.Styles
		p.AutoCleanupLevel = local.CleanupLevel
		res.PrefsSource = SourceLocal
	}

	var remote []RemoteItem
	if network && res.PrefsSource == SourceServer {
		err := auth.do(ctx, func(token string) error {
			items, err := s.Client.GetDictionary(ctx, token)
			remote = items
			return err
		})
		if err == nil {
			res.DictionarySource = SourceServer
		} else if res.Problem == "" {
			res.Problem = problemFor(err)
		}
	}
	if res.DictionarySource == SourceNone {
		items, err := ReadLocalDictionary(filepath.Join(a.Dir, FlowDBFileName))
		if err == nil {
			remote = items
			res.DictionarySource = SourceLocal
		} else if !errors.Is(err, os.ErrNotExist) {
			slog.Default().With("component", "wisprsettings").Warn("read local dictionary failed", "err", err)
		}
	}
	p.Dictionary, res.Excluded = ImportableEntries(remote, email)
	normalized, err := p.Normalize()
	if err == nil {
		p = normalized
	}
	res.Profile = p
	res.TokenRefreshed = auth.refreshed
	return res
}
