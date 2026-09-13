package basic

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"TcNo-Acc-Switcher/internal/appclient"
	"TcNo-Acc-Switcher/internal/fsutil"
	"TcNo-Acc-Switcher/internal/platform"
	"TcNo-Acc-Switcher/internal/security"
	"TcNo-Acc-Switcher/internal/winutil"
	"TcNo-Acc-Switcher/internal/wisprsettings"
	"TcNo-Acc-Switcher/internal/wisprstats"
)

const (
	wisprSettingsTimeout     = 2 * time.Minute
	wisprSwitchApplyTimeout  = 15 * time.Second
	wisprFlowExe             = "Wispr Flow.exe"
	wisprSettingsSettingsDir = "Settings"
)

type WisprSettingsAccount struct {
	UniqueID    string `json:"uniqueId"`
	DisplayName string `json:"displayName"`
	Current     bool   `json:"current"`
}

type WisprSettingsState struct {
	Profile  wisprsettings.Profile  `json:"profile"`
	Accounts []WisprSettingsAccount `json:"accounts"`
	Offline  bool                   `json:"offline"`
}

type WisprApplyReport struct {
	Results     []wisprsettings.AccountResult `json:"results"`
	Relaunched  bool                          `json:"relaunched"`
	CloseFailed bool                          `json:"closeFailed"`
	GeneratedAt string                        `json:"generatedAt"`
}

func (b *BasicService) GetWisprSettings() (WisprSettingsState, error) {
	if err := security.RequireUnlocked(); err != nil {
		return WisprSettingsState{}, err
	}
	profile, err := loadWisprSettingsProfile()
	if err != nil {
		return WisprSettingsState{}, err
	}
	accounts, err := b.savedWisprFlowAccounts()
	if err != nil {
		return WisprSettingsState{}, err
	}
	out := make([]WisprSettingsAccount, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, WisprSettingsAccount{UniqueID: a.UniqueID, DisplayName: a.DisplayName, Current: a.Current})
	}
	return WisprSettingsState{Profile: profile, Accounts: out, Offline: appclient.IsOfflineMode()}, nil
}

func (b *BasicService) SaveWisprSettings(profile wisprsettings.Profile) (wisprsettings.Profile, error) {
	if err := security.RequireUnlocked(); err != nil {
		return wisprsettings.Profile{}, err
	}
	path, err := wisprSettingsPath()
	if err != nil {
		return wisprsettings.Profile{}, err
	}
	return wisprsettings.SaveProfile(path, profile, func(path string, data []byte) error {
		return fsutil.WriteFileAtomic(path, data, 0o600)
	})
}

func (b *BasicService) ImportWisprSettings(uniqueID string) (wisprsettings.ImportResult, error) {
	if err := security.RequireUnlocked(); err != nil {
		return wisprsettings.ImportResult{}, err
	}
	accounts, err := b.savedWisprFlowAccounts()
	if err != nil {
		return wisprsettings.ImportResult{}, err
	}
	account, ok := findWisprAccount(accounts, uniqueID)
	if !ok {
		return wisprsettings.ImportResult{}, errors.New("unknown Wispr Flow account")
	}
	if !account.Current && security.SavedAccountDataEncrypted() {
		return wisprsettings.ImportResult{}, errors.New("saved account data is encrypted, switch to this account to import from it")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), wisprSettingsTimeout)
	defer cancel()
	allowRefresh := !account.Current || !winutil.IsExeRunning(wisprFlowExe)
	result := newWisprSyncer().Import(ctx, account, !appclient.IsOfflineMode(), allowRefresh)
	wisprSettingsLog().Info("wispr flow settings imported",
		"prefs", result.PrefsSource, "dictionary", result.DictionarySource,
		"entries", len(result.Profile.Dictionary), "excluded", result.Excluded, "problem", result.Problem)
	return result, nil
}

func (b *BasicService) ApplyWisprSettings() (WisprApplyReport, error) {
	if err := security.RequireUnlocked(); err != nil {
		return WisprApplyReport{}, err
	}
	profile, err := loadWisprSettingsProfile()
	if err != nil {
		return WisprApplyReport{}, err
	}
	if !profile.HasWork() {
		return WisprApplyReport{}, errors.New("turn on at least one part to apply")
	}
	accounts, err := b.savedWisprFlowAccounts()
	if err != nil {
		return WisprApplyReport{}, err
	}
	targets := make([]wisprstats.Account, 0, len(accounts))
	for _, a := range accounts {
		if profile.Targets(a.UniqueID) {
			targets = append(targets, a)
		}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), wisprSettingsTimeout)
	defer cancel()

	syncer := newWisprSyncer()
	encrypted := security.SavedAccountDataEncrypted()
	report := WisprApplyReport{Results: make([]wisprsettings.AccountResult, 0, len(targets))}
	closedWispr := false
	for _, a := range targets {
		opts := wisprsettings.ApplyOptions{AllowRefresh: true, LocalConfigWritable: true}
		if a.Current {
			running := winutil.IsExeRunning(wisprFlowExe)
			if running && profile.Parts.Voices {
				if err := b.closePlatformLocked(wisprFlowPlatformKey); err != nil {
					wisprSettingsLog().Warn("closing wispr flow before applying settings failed", "err", err)
					report.CloseFailed = true
				} else {
					closedWispr = true
					running = false
				}
			}
			opts = wisprsettings.ApplyOptions{AllowRefresh: !running, LocalConfigWritable: !running}
		} else if encrypted {
			report.Results = append(report.Results, encryptedResult(a, profile))
			continue
		}
		result := syncer.Apply(ctx, a, profile, opts)
		logWisprApplyResult("wispr flow settings applied", result)
		report.Results = append(report.Results, result)
	}
	if closedWispr {
		if err := launchBasicNoStatus(b.deps(), wisprFlowPlatformKey, nil); err != nil {
			wisprSettingsLog().Warn("relaunching wispr flow after applying settings failed", "err", err)
		} else {
			report.Relaunched = true
		}
		platform.EmitActionBarStatus("")
	}
	report.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	return report, nil
}

func applyWisprSettingsOnSwitch(platformKey, uniqueID, displayName string) {
	if !strings.EqualFold(strings.TrimSpace(platformKey), wisprFlowPlatformKey) {
		return
	}
	profile, err := loadWisprSettingsProfile()
	if err != nil {
		wisprSettingsLog().Warn("wispr flow settings unreadable, skipping apply on switch", "err", err)
		return
	}
	if !shouldApplyWisprOnSwitch(platformKey, uniqueID, profile, appclient.IsOfflineMode()) {
		return
	}
	platform.EmitActionBarStatusI18n("Status_WisprApplyingSettings")
	ctx, cancel := context.WithTimeout(context.Background(), wisprSwitchApplyTimeout)
	defer cancel()
	account := wisprstats.Account{UniqueID: uniqueID, DisplayName: displayName, Current: true, Dir: wisprFlowLiveDir()}
	result := newWisprSyncer().Apply(ctx, account, profile, wisprsettings.ApplyOptions{AllowRefresh: true, LocalConfigWritable: true})
	logWisprApplyResult("wispr flow settings applied on switch", result)
}

func shouldApplyWisprOnSwitch(platformKey, uniqueID string, profile wisprsettings.Profile, offline bool) bool {
	return strings.EqualFold(strings.TrimSpace(platformKey), wisprFlowPlatformKey) && profile.AppliesOnSwitchTo(uniqueID, offline)
}

func (b *BasicService) savedWisprFlowAccounts() ([]wisprstats.Account, error) {
	accounts, err := b.wisprFlowAccounts()
	if err != nil {
		return nil, err
	}
	ids, err := readIDs(wisprFlowPlatformKey)
	if err != nil {
		return nil, err
	}
	out := make([]wisprstats.Account, 0, len(accounts))
	for _, a := range accounts {
		if _, saved := ids[a.UniqueID]; saved {
			out = append(out, a)
		}
	}
	return out, nil
}

func findWisprAccount(accounts []wisprstats.Account, uniqueID string) (wisprstats.Account, bool) {
	uniqueID = strings.TrimSpace(uniqueID)
	for _, a := range accounts {
		if a.UniqueID == uniqueID {
			return a, true
		}
	}
	return wisprstats.Account{}, false
}

func encryptedResult(a wisprstats.Account, profile wisprsettings.Profile) wisprsettings.AccountResult {
	part := func(on bool) wisprsettings.PartResult {
		if !on {
			return wisprsettings.PartResult{Status: wisprsettings.StatusOff}
		}
		return wisprsettings.PartResult{Status: wisprsettings.StatusFailed, Problem: wisprsettings.ProblemEncrypted}
	}
	return wisprsettings.AccountResult{
		UniqueID:    a.UniqueID,
		DisplayName: a.DisplayName,
		Prefs:       part(profile.Parts.Styles || profile.Parts.AutoCleanup),
		Dictionary:  part(profile.Parts.Dictionary),
		Voices:      part(profile.Parts.Voices),
	}
}

func loadWisprSettingsProfile() (wisprsettings.Profile, error) {
	path, err := wisprSettingsPath()
	if err != nil {
		return wisprsettings.Profile{}, err
	}
	return wisprsettings.LoadProfile(path)
}

func wisprSettingsPath() (string, error) {
	dir, err := platform.EffectiveUserDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, wisprSettingsSettingsDir, wisprsettings.ProfileFileName), nil
}

func newWisprSyncer() *wisprsettings.Syncer {
	return wisprsettings.NewSyncer(func(path string, data []byte) error {
		return fsutil.WriteFileAtomic(path, data, 0o600)
	})
}

func logWisprApplyResult(msg string, r wisprsettings.AccountResult) {
	wisprSettingsLog().Info(msg,
		"current", r.Current,
		"prefs", r.Prefs.Status, "prefsProblem", r.Prefs.Problem,
		"dictionary", r.Dictionary.Status, "dictionaryProblem", r.Dictionary.Problem,
		"added", r.Dictionary.Added, "updated", r.Dictionary.Updated,
		"voices", r.Voices.Status, "voicesProblem", r.Voices.Problem,
		"tokenRefreshed", r.TokenRefreshed)
}

func wisprSettingsLog() *slog.Logger {
	return slog.Default().With("component", "wisprsettings")
}
