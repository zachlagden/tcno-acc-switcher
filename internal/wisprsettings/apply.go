package wisprsettings

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"TcNo-Acc-Switcher/internal/wisprstats"

	"github.com/google/uuid"
)

const (
	StatusOff       = "off"
	StatusApplied   = "applied"
	StatusUnchanged = "unchanged"
	StatusFailed    = "failed"

	ProblemSignedOut      = wisprstats.ProblemSignedOut
	ProblemSessionExpired = wisprstats.ProblemSessionExpired
	ProblemCurrentExpired = wisprstats.ProblemCurrentExpired
	ProblemSessionFile    = wisprstats.ProblemSessionFile
	ProblemNetwork        = wisprstats.ProblemNetwork
	ProblemConflict       = "conflict"
	ProblemServer         = "server"
	ProblemConfigFile     = "config_file"
	ProblemNoConfig       = "no_config"
	ProblemWisprRunning   = "wispr_running"
	ProblemEncrypted      = "encrypted"
	ProblemCloseFailed    = "close_failed"

	tokenMarginSeconds = 60
	FlowDBFileName     = "flow.sqlite"
)

var errCurrentExpired = errors.New("wisprsettings: the live session needs Wispr to renew it")

type PartResult struct {
	Status  string `json:"status"`
	Problem string `json:"problem"`
	Added   int    `json:"added"`
	Updated int    `json:"updated"`
}

type AccountResult struct {
	UniqueID       string     `json:"uniqueId"`
	DisplayName    string     `json:"displayName"`
	Current        bool       `json:"current"`
	Prefs          PartResult `json:"prefs"`
	Dictionary     PartResult `json:"dictionary"`
	Voices         PartResult `json:"voices"`
	TokenRefreshed bool       `json:"tokenRefreshed"`
}

type ApplyOptions struct {
	AllowRefresh        bool
	LocalConfigWritable bool
}

type Syncer struct {
	Client    *Client
	Tokens    *wisprstats.Client
	Now       func() time.Time
	NewID     func() string
	WriteFile func(path string, data []byte) error
}

func NewSyncer(write func(path string, data []byte) error) *Syncer {
	return &Syncer{Client: NewClient(), Tokens: wisprstats.NewClient(), WriteFile: write}
}

func (s *Syncer) Apply(ctx context.Context, a wisprstats.Account, p Profile, opts ApplyOptions) AccountResult {
	res := AccountResult{
		UniqueID:    a.UniqueID,
		DisplayName: a.DisplayName,
		Current:     a.Current,
		Prefs:       PartResult{Status: StatusOff},
		Dictionary:  PartResult{Status: StatusOff},
		Voices:      PartResult{Status: StatusOff},
	}
	auth := &accountAuth{s: s, acct: a, allowRefresh: opts.AllowRefresh}
	if p.Parts.Styles || p.Parts.AutoCleanup {
		res.Prefs = s.applyPrefs(ctx, auth, a, p, opts)
	}
	if p.Parts.Dictionary {
		res.Dictionary = s.applyDictionary(ctx, auth, p)
	}
	if p.Parts.Voices {
		res.Voices = s.applyVoices(a, p, opts)
	}
	res.TokenRefreshed = auth.refreshed
	return res
}

func (s *Syncer) applyPrefs(ctx context.Context, auth *accountAuth, a wisprstats.Account, p Profile, opts ApplyOptions) PartResult {
	var merge PrefsMerge
	var modifiedAt string
	status := StatusUnchanged
	err := auth.do(ctx, func(token string) error {
		for attempt := 0; attempt < 2; attempt++ {
			server, err := s.Client.GetPrefs(ctx, token)
			if err != nil {
				return err
			}
			merge, err = MergePrefs(server, p)
			if err != nil {
				return err
			}
			if !merge.Changed {
				modifiedAt = server.ModifiedAt
				return nil
			}
			modifiedAt, err = s.Client.PostPrefs(ctx, token, merge.Body)
			if errors.Is(err, ErrConflict) {
				continue
			}
			if err != nil {
				return err
			}
			status = StatusApplied
			return nil
		}
		return ErrConflict
	})
	if err != nil {
		return PartResult{Status: StatusFailed, Problem: problemFor(err)}
	}
	res := PartResult{Status: status}
	if !opts.LocalConfigWritable {
		return res
	}
	path := filepath.Join(a.Dir, wisprstats.ConfigFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return res
	}
	if err == nil {
		data, err = SetCachedPrefs(data, merge, modifiedAt)
	}
	if err == nil {
		err = s.WriteFile(path, data)
	}
	if err != nil {
		res.Problem = ProblemConfigFile
	}
	return res
}

func (s *Syncer) applyDictionary(ctx context.Context, auth *accountAuth, p Profile) PartResult {
	var plan DictionaryPlan
	err := auth.do(ctx, func(token string) error {
		remote, err := s.Client.GetDictionary(ctx, token)
		if err != nil {
			return err
		}
		plan, err = PlanDictionary(remote, p.Dictionary, s.now(), s.newID)
		if err != nil || len(plan.Upserts) == 0 {
			return err
		}
		return s.Client.PostDictionary(ctx, token, plan.Upserts)
	})
	if err != nil {
		return PartResult{Status: StatusFailed, Problem: problemFor(err)}
	}
	if len(plan.Upserts) == 0 {
		return PartResult{Status: StatusUnchanged}
	}
	return PartResult{Status: StatusApplied, Added: plan.Added, Updated: plan.Updated}
}

func (s *Syncer) applyVoices(a wisprstats.Account, p Profile, opts ApplyOptions) PartResult {
	if !opts.LocalConfigWritable {
		return PartResult{Status: StatusFailed, Problem: ProblemWisprRunning}
	}
	path := filepath.Join(a.Dir, wisprstats.ConfigFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return PartResult{Status: StatusFailed, Problem: ProblemNoConfig}
	}
	if err != nil {
		return PartResult{Status: StatusFailed, Problem: ProblemConfigFile}
	}
	if VoicesMatch(data, p.UserVoices) {
		return PartResult{Status: StatusUnchanged}
	}
	updated, err := SetVoices(data, p.UserVoices)
	if err == nil {
		err = s.WriteFile(path, updated)
	}
	if err != nil {
		return PartResult{Status: StatusFailed, Problem: ProblemConfigFile}
	}
	return PartResult{Status: StatusApplied}
}

type accountAuth struct {
	s            *Syncer
	acct         wisprstats.Account
	allowRefresh bool
	loaded       bool
	token        string
	refreshed    bool
	email        string
}

func (a *accountAuth) load() error {
	if a.loaded {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(a.acct.Dir, wisprstats.SessionFileName))
	if err != nil {
		return wisprstats.ErrNoSession
	}
	session, err := wisprstats.ParseSession(data)
	if err != nil {
		return wisprstats.ErrNoSession
	}
	a.loaded = true
	a.email = session.Email
	if session.ValidAt(a.s.now().Unix(), tokenMarginSeconds) {
		a.token = session.AccessToken
	}
	return nil
}

func (a *accountAuth) do(ctx context.Context, fn func(token string) error) error {
	if err := a.load(); err != nil {
		return err
	}
	if a.token != "" {
		err := fn(a.token)
		if !errors.Is(err, wisprstats.ErrUnauthorized) || a.refreshed {
			return err
		}
	}
	if !a.allowRefresh {
		return errCurrentExpired
	}
	if err := a.refresh(ctx); err != nil {
		return err
	}
	return fn(a.token)
}

func (a *accountAuth) refresh(ctx context.Context) error {
	path := filepath.Join(a.acct.Dir, wisprstats.SessionFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return wisprstats.ErrNoSession
	}
	session, err := wisprstats.ParseSession(data)
	if err != nil {
		return wisprstats.ErrNoSession
	}
	tok, err := a.s.Tokens.Refresh(ctx, session.RefreshToken)
	if err != nil {
		return err
	}
	updated, err := wisprstats.ApplyRefresh(data, tok, a.s.now().Unix())
	if err != nil {
		return errSessionFile
	}
	if err := a.s.WriteFile(path, updated); err != nil {
		return errSessionFile
	}
	a.token = tok.AccessToken
	a.refreshed = true
	return nil
}

var errSessionFile = errors.New("wisprsettings: could not update the saved session")

func problemFor(err error) string {
	switch {
	case errors.Is(err, wisprstats.ErrNoSession):
		return ProblemSignedOut
	case errors.Is(err, errCurrentExpired):
		return ProblemCurrentExpired
	case errors.Is(err, wisprstats.ErrSessionExpired), errors.Is(err, wisprstats.ErrUnauthorized):
		return ProblemSessionExpired
	case errors.Is(err, errSessionFile):
		return ProblemSessionFile
	case errors.Is(err, ErrConflict):
		return ProblemConflict
	case errors.Is(err, ErrNoServerPrefs), errors.Is(err, ErrBadServerReply):
		return ProblemServer
	default:
		return ProblemNetwork
	}
}

func (s *Syncer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Syncer) newID() string {
	if s.NewID != nil {
		return s.NewID()
	}
	return uuid.NewString()
}
