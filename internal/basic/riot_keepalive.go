package basic

import (
	"context"
	"log/slog"

	"TcNo-Acc-Switcher/internal/platform"
	"TcNo-Acc-Switcher/internal/riotkeepalive"
)

// riotPlatformKey matches the Platforms.json entry.
const riotPlatformKey = "Riot Games"

// riotClientRunning reports whether any Riot process is up.
//
// It fails safe: if the descriptor cannot be read we return true, which stops the
// sweep. Refreshing while a client holds the same token is the one failure mode that
// could actually cost a session, so "don't know" must mean "don't touch".
func riotClientRunning() bool {
	descriptor, _, err := readDescriptor(riotPlatformKey)
	if err != nil {
		riotKeepaliveLog().Debug("riot keepalive: descriptor unreadable, assuming client is up", "err", err)
		return true
	}
	if len(descriptor.ExesToEnd) == 0 {
		return true
	}
	for _, exe := range descriptor.ExesToEnd {
		if isGameProcessRunning(exe) {
			return true
		}
	}
	return false
}

// riotLiveSettingsPath resolves the Riot Client's in-use settings file.
//
// Derived from the descriptor's LoginFiles rather than hardcoded, so it follows
// Platforms.json if Riot moves the file again. Falls back to the known location if
// the descriptor cannot be read.
func riotLiveSettingsPath() string {
	if descriptor, _, err := readDescriptor(riotPlatformKey); err == nil {
		for liveKey, cacheRel := range descriptor.LoginFiles {
			if cacheRel == riotkeepalive.SettingsFileName {
				return platform.ExpandWindowsPath(liveKey)
			}
		}
	}
	return platform.ExpandWindowsPath(
		`%LocalAppData%\Riot Games\Riot Client\Data\` + riotkeepalive.SettingsFileName)
}

func riotKeepaliveLog() *slog.Logger {
	return slog.Default().With("component", "riotkeepalive")
}

// newRiotSweeper builds a Sweeper over the Riot login cache, or nil if the cache
// path cannot be resolved.
func (b *BasicService) newRiotSweeper() *riotkeepalive.Sweeper {
	root, err := loginCacheRoot(riotPlatformKey)
	if err != nil {
		riotKeepaliveLog().Warn("riot keepalive disabled: no login cache path", "err", err)
		return nil
	}
	live := riotLiveSettingsPath()
	if live == "" {
		riotKeepaliveLog().Warn("riot keepalive disabled: no live settings path")
		return nil
	}
	return &riotkeepalive.Sweeper{
		CacheRoot:        root,
		LiveSettingsPath: live,
		Client:           riotkeepalive.NewClient(),
		Interval:         riotkeepalive.DefaultInterval,
		Spacing:          riotkeepalive.DefaultSpacing,
		Log:              riotKeepaliveLog(),
		ClientRunning:    riotClientRunning,
	}
}

// StartRiotKeepalive runs the periodic refresh in the background until ctx is
// cancelled. Calling it again replaces any previous sweeper.
func (b *BasicService) StartRiotKeepalive(ctx context.Context) {
	sweeper := b.newRiotSweeper()
	if sweeper == nil {
		return
	}

	b.mu.Lock()
	if b.riotKeepaliveCancel != nil {
		b.riotKeepaliveCancel()
	}
	sweepCtx, cancel := context.WithCancel(ctx)
	b.riotKeepaliveCancel = cancel
	b.mu.Unlock()

	go sweeper.Run(sweepCtx)
}

// StopRiotKeepalive halts the background sweeper.
func (b *BasicService) StopRiotKeepalive() {
	b.mu.Lock()
	cancel := b.riotKeepaliveCancel
	b.riotKeepaliveCancel = nil
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// RiotKeepaliveSweepNow refreshes every saved Riot account once, on demand.
// Exposed so the UI can offer a manual "refresh sessions" action.
func (b *BasicService) RiotKeepaliveSweepNow(ctx context.Context) ([]riotkeepalive.Result, error) {
	sweeper := b.newRiotSweeper()
	if sweeper == nil {
		return nil, nil
	}
	return sweeper.SweepOnce(ctx)
}
