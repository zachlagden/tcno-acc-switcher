package riotkeepalive

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func newTestClient(h http.HandlerFunc) (*Client, *httptest.Server) {
	srv := httptest.NewServer(h)
	c := NewClient()
	c.HTTP = srv.Client()
	c.Endpoint = srv.URL
	return c, srv
}

func TestRefreshSuccess(t *testing.T) {
	var gotForm url.Values
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT","id_token":"IT","refresh_token":"RT2","expires_in":3600}`))
	})
	defer srv.Close()

	tok, err := c.Refresh(context.Background(), "RT1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.RefreshToken != "RT2" || tok.IDToken != "IT" {
		t.Fatalf("bad token response: %+v", tok)
	}
	if gotForm.Get("grant_type") != "refresh_token" {
		t.Fatalf("wrong grant_type: %q", gotForm.Get("grant_type"))
	}
	if gotForm.Get("client_id") != ClientID {
		t.Fatalf("wrong client_id: %q", gotForm.Get("client_id"))
	}
	if gotForm.Get("refresh_token") != "RT1" {
		t.Fatal("refresh token not sent")
	}
}

func TestRefreshInvalidGrantIsTerminal(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"revoked"}`))
	})
	defer srv.Close()

	if _, err := c.Refresh(context.Background(), "RT1"); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("want ErrSessionExpired, got %v", err)
	}
}

func TestRefreshCDNRejectionIsBlocked(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html>denied</html>"))
	})
	defer srv.Close()

	if _, err := c.Refresh(context.Background(), "RT1"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("want ErrBlocked, got %v", err)
	}
}

func TestRefreshRejectsResponseWithoutRotatedToken(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT","id_token":"IT"}`))
	})
	defer srv.Close()

	if _, err := c.Refresh(context.Background(), "RT1"); err == nil {
		t.Fatal("expected refusal when no rotated refresh token is returned")
	}
}

func TestRefreshRejectsEmptyInput(t *testing.T) {
	if _, err := NewClient().Refresh(context.Background(), "  "); err == nil {
		t.Fatal("expected refusal on empty refresh token")
	}
}
