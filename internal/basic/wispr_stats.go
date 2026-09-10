package basic

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"TcNo-Acc-Switcher/internal/fsutil"
	"TcNo-Acc-Switcher/internal/platform"
	"TcNo-Acc-Switcher/internal/security"
	"TcNo-Acc-Switcher/internal/wisprstats"
)

const wisprFlowPlatformKey = "Wispr Flow"

const wisprStatsTimeout = 2 * time.Minute

func (b *BasicService) GetWisprFlowStats(live bool) (wisprstats.Report, error) {
	if err := security.RequireUnlocked(); err != nil {
		return wisprstats.Report{}, err
	}
	accounts, err := b.wisprFlowAccounts()
	if err != nil {
		return wisprstats.Report{}, err
	}
	if live {
		b.mu.Lock()
		defer b.mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), wisprStatsTimeout)
	defer cancel()
	collector := &wisprstats.Collector{
		Client: wisprstats.NewClient(),
		WriteFile: func(path string, data []byte) error {
			return fsutil.WriteFileAtomic(path, data, 0o600)
		},
	}
	report := collector.Collect(ctx, accounts, live)
	wisprStatsLog().Info("wispr flow stats collected", "accounts", len(report.Accounts), "live", live)
	return report, nil
}

func (b *BasicService) wisprFlowAccounts() ([]wisprstats.Account, error) {
	list, err := b.GetAccountsList(wisprFlowPlatformKey)
	if err != nil {
		return nil, err
	}
	ids, err := readIDs(wisprFlowPlatformKey)
	if err != nil {
		return nil, err
	}
	liveDir := wisprFlowLiveDir()

	accounts := make([]wisprstats.Account, 0, len(list)+1)
	hasCurrent := false
	for _, row := range list {
		name := strings.TrimSpace(ids[row.UniqueID])
		if name == "" {
			continue
		}
		dir, err := accountCacheDir(wisprFlowPlatformKey, name)
		if err != nil {
			return nil, err
		}
		if row.CurrentSession && liveDir != "" {
			dir = liveDir
			hasCurrent = true
		}
		accounts = append(accounts, wisprstats.Account{
			UniqueID:    row.UniqueID,
			DisplayName: row.DisplayName,
			Current:     row.CurrentSession,
			Dir:         dir,
		})
	}
	if !hasCurrent && liveDir != "" {
		if current, ok := b.unsavedWisprFlowAccount(liveDir); ok {
			accounts = append(accounts, current)
		}
	}
	return accounts, nil
}

func (b *BasicService) unsavedWisprFlowAccount(liveDir string) (wisprstats.Account, bool) {
	d, _, err := readDescriptor(wisprFlowPlatformKey)
	if err != nil {
		return wisprstats.Account{}, false
	}
	folder, _ := resolveExeFolder(b.deps(), wisprFlowPlatformKey)
	uid, err := ReadUniqueID(wisprFlowPlatformKey, d, folder)
	if err != nil || strings.TrimSpace(uid) == "" {
		return wisprstats.Account{}, false
	}
	ctx := platform.PathTokenContext{PlatformFolder: folder}
	vars := resolveDescriptorVariables(d, folder, ctx, "", false)
	name := strings.TrimSpace(resolveDescriptorValue(d, d.Extras.BuiltInUsernameFile, folder, ctx, vars, "", false))
	if name == "" {
		name = uid
	}
	return wisprstats.Account{UniqueID: uid, DisplayName: name, Current: true, Dir: liveDir}, true
}

func wisprFlowLiveDir() string {
	if d, _, err := readDescriptor(wisprFlowPlatformKey); err == nil {
		for liveKey, cacheRel := range d.LoginFiles {
			if cacheRel == wisprstats.SessionFileName {
				return filepath.Dir(platform.ExpandWindowsPath(liveKey))
			}
		}
	}
	return platform.ExpandWindowsPath(`%AppData%\Wispr Flow`)
}

func wisprStatsLog() *slog.Logger {
	return slog.Default().With("component", "wisprstats")
}
