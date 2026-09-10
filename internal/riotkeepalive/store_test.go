package riotkeepalive

import (
	"bytes"
	"strings"
	"testing"
)

// fixture mirrors a real file: CRLF endings, four-space indent, tokens elided.
func fixture() []byte {
	lines := []string{
		"psl:",
		"    authorization:",
		"        riot-client:",
		"            claims: []",
		"            id_token: OLD_ID_TOKEN",
		"            is_dpop_bound: false",
		"            last_token_creation_time: 1788696389691",
		"            original_token_creation_time: 1788696389691",
		"            refresh_token: OLD_REFRESH_TOKEN",
		"            refresh_token_write_count: 1",
		`            refresh_tokens_session_id: "17f3db81-b00a-4043-b486-7f1a0dfdc197"`,
		"            scopes:",
		"                - openid",
		"                - account",
		"riot-login:",
		"    persist: null",
		"rso-authenticator:",
		"    tdid:",
		"        name: tdid",
		"        value: TDID_VALUE",
		"",
	}
	return []byte(strings.Join(lines, "\r\n"))
}

func TestParseRealShape(t *testing.T) {
	s, err := Parse(fixture())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.RefreshToken != "OLD_REFRESH_TOKEN" || s.IDToken != "OLD_ID_TOKEN" {
		t.Fatalf("wrong tokens: %+v", s)
	}
	if s.WriteCount != 1 || s.LastCreation != 1788696389691 {
		t.Fatalf("wrong metadata: %+v", s)
	}
}

func TestParseNoSession(t *testing.T) {
	// This is the shape produced when "Stay signed in" is off.
	data := []byte("psl:\r\n    authorization:\r\n        riot-client: null\r\nriot-login:\r\n    persist: null\r\n")
	if _, err := Parse(data); err != ErrNoSession {
		t.Fatalf("want ErrNoSession, got %v", err)
	}
}

func TestParseDPoPBoundRefused(t *testing.T) {
	data := bytes.Replace(fixture(),
		[]byte("is_dpop_bound: false"), []byte("is_dpop_bound: true"), 1)
	if _, err := Parse(data); err != ErrDPoPBound {
		t.Fatalf("want ErrDPoPBound, got %v", err)
	}
}

func TestApplyPreservesFormatting(t *testing.T) {
	in := fixture()
	out, err := Apply(in, &TokenResponse{
		RefreshToken: "NEW_REFRESH_TOKEN",
		IDToken:      "NEW_ID_TOKEN",
	}, 1788700000000)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if bytes.Contains(out, []byte("OLD_REFRESH_TOKEN")) || bytes.Contains(out, []byte("OLD_ID_TOKEN")) {
		t.Fatal("old tokens survived the rewrite")
	}
	// CRLF must survive: a bare LF would mean we rewrote line endings.
	if n := bytes.Count(out, []byte("\r\n")); n != bytes.Count(in, []byte("\r\n")) {
		t.Fatalf("CRLF count changed: %d -> %d", bytes.Count(in, []byte("\r\n")), n)
	}
	if !bytes.Contains(out, []byte("            refresh_token: NEW_REFRESH_TOKEN\r\n")) {
		t.Fatal("indentation not preserved on refresh_token")
	}
	// Untouched regions must be byte-identical.
	for _, keep := range []string{
		`            refresh_tokens_session_id: "17f3db81-b00a-4043-b486-7f1a0dfdc197"`,
		"        value: TDID_VALUE",
		"    persist: null",
	} {
		if !bytes.Contains(out, []byte(keep)) {
			t.Fatalf("unrelated line was altered: %q", keep)
		}
	}

	s, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if s.WriteCount != 2 {
		t.Fatalf("write count not incremented: %d", s.WriteCount)
	}
	if s.LastCreation != 1788700000000 {
		t.Fatalf("creation time not updated: %d", s.LastCreation)
	}
}

func TestApplyRefusesEmptyToken(t *testing.T) {
	if _, err := Apply(fixture(), &TokenResponse{RefreshToken: ""}, 1); err == nil {
		t.Fatal("expected refusal on empty refresh token")
	}
}

func TestApplyRefusesUnknownShape(t *testing.T) {
	if _, err := Apply([]byte("something: else\r\n"), &TokenResponse{RefreshToken: "x"}, 1); err == nil {
		t.Fatal("expected refusal when there is no session to update")
	}
}

func TestSetScalarRejectsAmbiguity(t *testing.T) {
	data := []byte("a:\n    refresh_token: one\nb:\n    refresh_token: two\n")
	if _, err := setScalar(data, "refresh_token", "x"); err == nil {
		t.Fatal("expected an error when the key is ambiguous")
	}
}
