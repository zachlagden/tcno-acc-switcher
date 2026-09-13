package basic

import (
	"testing"

	"TcNo-Acc-Switcher/internal/wisprsettings"
)

func TestShouldApplyWisprOnSwitch(t *testing.T) {
	p := wisprsettings.DefaultProfile()
	p.Parts.Dictionary = true
	p.ApplyOnSwitch = true
	cases := []struct {
		name     string
		platform string
		uid      string
		mutate   func(*wisprsettings.Profile)
		offline  bool
		want     bool
	}{
		{"targeted", "Wispr Flow", "a", nil, false, true},
		{"other platform", "Discord", "a", nil, false, false},
		{"offline", "Wispr Flow", "a", nil, true, false},
		{"disabled", "Wispr Flow", "a", func(p *wisprsettings.Profile) { p.ApplyOnSwitch = false }, false, false},
		{"not selected", "Wispr Flow", "a", func(p *wisprsettings.Profile) {
			p.Target = wisprsettings.TargetSelected
			p.SelectedAccounts = []string{"b"}
		}, false, false},
		{"no parts", "Wispr Flow", "a", func(p *wisprsettings.Profile) { p.Parts = wisprsettings.Parts{} }, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := p
			if c.mutate != nil {
				c.mutate(&q)
			}
			if got := shouldApplyWisprOnSwitch(c.platform, c.uid, q, c.offline); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}
