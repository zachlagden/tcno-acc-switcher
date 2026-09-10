package riotkeepalive

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// DefaultInterval is how often saved accounts are topped up while the app runs.
//
// The id_token is good for an hour, but that is not what we are protecting — the
// refresh token's own lifetime is opaque (it is a JWE) and is what eventually
// expires. Refreshing more often than necessary is not free: every refresh rotates
// the token, and every rotation is another chance to race a running client. Twelve
// hours keeps accounts warm without churning.
const DefaultInterval = 12 * time.Hour

// DefaultSpacing separates per-account requests so a sweep across many accounts does
// not arrive as a burst.
const DefaultSpacing = 3 * time.Second

// DefaultInitialDelay is how long after start-up the first sweep runs.
//
// There has to be a first sweep before the first tick. Most sessions with this app are
// short — open it, switch account, close it — so a ticker-only design would mean the
// keepalive effectively never runs for the people who need it. The delay exists only
// to let a client that is launching alongside us settle; the running-client guard, not
// this delay, is what actually keeps us off a live session.
const DefaultInitialDelay = 60 * time.Second

// ErrNoGuard is returned when a Sweeper is used without a running-client check.
// This is deliberately fatal rather than defaulting to "no guard": refreshing an
// account while Riot Client holds the same token is the one way this tool could
// actually cost someone a session.
var ErrNoGuard = errors.New("riotkeepalive: Sweeper.ClientRunning must be set")

// ErrClientRunning means the sweep was skipped because Riot was up.
var ErrClientRunning = errors.New("riotkeepalive: Riot Client is running, skipping sweep")

// ErrNoLivePath is returned when a Sweeper has no path to the live settings file.
//
// Required for the same reason as ErrNoGuard. One saved account is normally also the
// account currently deployed to the live file, holding the very same refresh token. If
// we refresh the saved copy without knowing that, the live copy is silently left
// holding a superseded token, and the next launch replays it.
var ErrNoLivePath = errors.New("riotkeepalive: Sweeper.LiveSettingsPath must be set")

// Target is one saved account carrying a persisted session.
type Target struct {
	AccountName  string
	SettingsPath string
}

// Result is the per-account outcome of a sweep.
type Result struct {
	Target
	Refreshed bool
	// Deployed reports that this account's token was also sitting in the live
	// settings file, so both copies were rewritten together.
	Deployed bool
	// NeedsLogin is set when Riot rejected the token outright. Callers should
	// surface this in the UI — no retry will recover it.
	NeedsLogin bool
	Err        error
}

// CollectTargets walks a platform's login cache and returns every saved account that
// holds a Riot settings file. Accounts without one are skipped silently: an account
// saved before "Stay signed in" was ticked simply has nothing to refresh.
func CollectTargets(cacheRoot string) ([]Target, error) {
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("riotkeepalive: read login cache: %w", err)
	}

	var targets []Target
	for _, e := range entries {
		if !e.IsDir() {
			continue // ids.json, ordering files, etc.
		}
		accountDir := filepath.Join(cacheRoot, e.Name())
		found := ""
		_ = filepath.WalkDir(accountDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || found != "" {
				return nil
			}
			if !d.IsDir() && d.Name() == SettingsFileName {
				found = path
			}
			return nil
		})
		if found != "" {
			targets = append(targets, Target{AccountName: e.Name(), SettingsPath: found})
		}
	}
	return targets, nil
}

// Sweeper periodically refreshes every saved Riot account.
type Sweeper struct {
	CacheRoot string
	// LiveSettingsPath is the Riot Client's in-use settings file. Required — see
	// ErrNoLivePath.
	LiveSettingsPath string
	Client           *Client
	Interval         time.Duration
	Spacing          time.Duration
	// InitialDelay is the wait before the first sweep. Defaults to DefaultInitialDelay.
	InitialDelay time.Duration
	Log          *slog.Logger

	// ClientRunning reports whether any Riot process is up. Required — see ErrNoGuard.
	ClientRunning func() bool

	// now and sleep exist so tests do not have to wait.
	now   func() time.Time
	sleep func(context.Context, time.Duration)
}

func (s *Sweeper) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Sweeper) timeNow() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Sweeper) pause(ctx context.Context, d time.Duration) {
	if s.sleep != nil {
		s.sleep(ctx, d)
		return
	}
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// Run sweeps shortly after start and then on a ticker, until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	initial := s.InitialDelay
	if initial <= 0 {
		initial = DefaultInitialDelay
	}

	s.pause(ctx, initial)
	if ctx.Err() != nil {
		return
	}
	s.sweepAndLog(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepAndLog(ctx)
		}
	}
}

func (s *Sweeper) sweepAndLog(ctx context.Context) {
	results, err := s.SweepOnce(ctx)
	if err != nil {
		if errors.Is(err, ErrClientRunning) {
			s.logger().Debug("riot keepalive skipped", "reason", "client running")
			return
		}
		s.logger().Warn("riot keepalive sweep failed", "err", err)
		return
	}
	var ok, expired, failed int
	for _, r := range results {
		switch {
		case r.Refreshed:
			ok++
		case r.NeedsLogin:
			expired++
		case r.Err != nil:
			failed++
		}
	}
	if ok+expired+failed == 0 {
		return
	}
	s.logger().Info("riot keepalive sweep complete",
		"refreshed", ok, "needs_login", expired, "failed", failed)
}

// SweepOnce refreshes every saved account exactly once.
//
// It refuses to run at all while Riot is up. Riot rotates the refresh token on every
// use, so a client refreshing concurrently would leave one side holding a superseded
// token — which reuse detection can read as theft and answer by revoking the family.
func (s *Sweeper) SweepOnce(ctx context.Context) ([]Result, error) {
	if s.ClientRunning == nil {
		return nil, ErrNoGuard
	}
	if s.LiveSettingsPath == "" {
		return nil, ErrNoLivePath
	}
	if s.ClientRunning() {
		return nil, ErrClientRunning
	}

	targets, err := CollectTargets(s.CacheRoot)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, nil
	}

	client := s.Client
	if client == nil {
		client = NewClient()
	}
	spacing := s.Spacing
	if spacing <= 0 {
		spacing = DefaultSpacing
	}

	results := make([]Result, 0, len(targets))
	for i, t := range targets {
		if err := ctx.Err(); err != nil {
			break
		}
		if i > 0 {
			s.pause(ctx, spacing)
		}
		// Re-check between accounts: the user may have launched a game mid-sweep.
		if s.ClientRunning() {
			s.logger().Debug("riot keepalive halted mid-sweep", "reason", "client started")
			break
		}
		results = append(results, s.refreshOne(ctx, client, t))
	}
	return results, nil
}

func (s *Sweeper) refreshOne(ctx context.Context, client *Client, t Target) Result {
	res := Result{Target: t}

	data, err := os.ReadFile(t.SettingsPath)
	if err != nil {
		res.Err = err
		return res
	}
	sess, err := Parse(data)
	if err != nil {
		// No session, or a DPoP-bound one: nothing to do, and not alarming.
		res.Err = err
		return res
	}

	// Work out whether this saved account is also the one currently deployed. If the
	// live file carries the same refresh token, the two files are the same session and
	// must be rewritten together or not at all.
	liveData, deployed, err := s.liveState(sess.RefreshToken)
	if err != nil {
		// We cannot tell. Refreshing now might strand the live copy, so don't.
		res.Err = fmt.Errorf("skipped, live settings file unreadable: %w", err)
		return res
	}
	res.Deployed = deployed

	tok, err := client.Refresh(ctx, sess.RefreshToken)
	if err != nil {
		res.Err = err
		res.NeedsLogin = errors.Is(err, ErrSessionExpired)
		return res
	}

	// From here the old token is dead and only the new one is usable. Every copy of
	// it has to be updated, and the live file goes first: if the second write fails,
	// a stale saved copy is recoverable on the next swap-out, whereas a stale live
	// copy breaks the very next launch.
	now := s.timeNow().UnixMilli()
	if deployed {
		updatedLive, err := Apply(liveData, tok, now)
		if err == nil {
			err = writeFileAtomic(s.LiveSettingsPath, updatedLive)
		}
		if err != nil {
			res.Err = fmt.Errorf("refreshed but the live settings file is now stale "+
				"(sign in again if the client rejects the session): %w", err)
			return res
		}
	}

	updated, err := Apply(data, tok, now)
	if err == nil {
		err = writeFileAtomic(t.SettingsPath, updated)
	}
	if err != nil {
		res.Err = fmt.Errorf("refreshed but could not persist to the saved account "+
			"(its stored token is now stale; re-save it before switching to it): %w", err)
		return res
	}

	res.Refreshed = true
	return res
}

// liveState reports whether the live settings file holds the given refresh token,
// returning its bytes so a matching account can be rewritten in step.
//
// A live file that is missing, or that holds no session at all, simply means nothing
// is deployed — that is not an error. Anything else is, because "unknown" must not be
// treated as "not deployed".
func (s *Sweeper) liveState(savedToken string) (data []byte, deployed bool, err error) {
	data, err = os.ReadFile(s.LiveSettingsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	live, err := Parse(data)
	if err != nil {
		if errors.Is(err, ErrNoSession) || errors.Is(err, ErrDPoPBound) {
			return data, false, nil
		}
		return nil, false, err
	}
	return data, live.RefreshToken == savedToken, nil
}

// writeFileAtomic writes via a temp file in the same directory and renames over the
// target, so a crash mid-write cannot leave a saved account holding half a token.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".riotkeepalive-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		_ = os.Chmod(tmp, info.Mode())
	}
	return os.Rename(tmp, path)
}
