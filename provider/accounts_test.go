package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountBindingIndependentHeadersAndSnapshots(t *testing.T) {
	a := Account{ID: "work", ProviderID: "chatgpt", Label: "Work", OAuth: &OAuthCredentials{AccessToken: "work-token", RefreshToken: "work-refresh", AccountID: "org-work", ExpiresAt: time.Now().Add(time.Hour).Unix()}}
	b := Account{ID: "home", ProviderID: "chatgpt", Label: "Home", OAuth: &OAuthCredentials{AccessToken: "home-token", RefreshToken: "home-refresh", AccountID: "org-home", ExpiresAt: time.Now().Add(time.Hour).Unix()}}
	m := NewAccountManager(map[string]Account{a.ID: a, b.ID: b}, nil)
	a.OAuth.AccessToken = "input-mutated"
	snapshot := m.Snapshot()
	snapshot["work"].OAuth.AccountID = "snapshot-mutated"
	one, _ := m.Account("work")
	one.OAuth.AccessToken = "account-mutated"

	work := FromPreset("chatgpt", "wrong-transient", "")
	home := FromPreset("chatgpt", "", "")
	if err := m.Bind(work, "work"); err != nil {
		t.Fatal(err)
	}
	if err := m.Bind(home, "home"); err != nil {
		t.Fatal(err)
	}
	if work.AccountID != "work" || home.AccountID != "home" {
		t.Fatalf("runtime identities: %q, %q", work.AccountID, home.AccountID)
	}
	var wg sync.WaitGroup
	for _, tc := range []struct {
		p          *Provider
		token, org string
	}{{work, "work-token", "org-work"}, {home, "home-token", "org-home"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				h, err := tc.p.RequestHeaders(context.Background())
				if err != nil || h.Get("Authorization") != "Bearer "+tc.token || h.Get("chatgpt-account-id") != tc.org || h.Get("session-id") == "" || h.Get("session-id") != h.Get("thread-id") {
					t.Errorf("account %q headers %v, err %v", tc.p.AccountID, h, err)
				}
			}
		}()
	}
	wg.Wait()
	if err := m.Bind(FromPreset("openai", "", ""), "work"); err == nil {
		t.Fatal("bound an account to the wrong provider")
	}
	// Renaming one account must not invalidate a session or change credentials.
	current := m.Snapshot()
	workAccount := current["work"]
	workAccount.Label = "Renamed"
	current["work"] = workAccount
	m.Replace(current)
	if h, err := work.RequestHeaders(context.Background()); err != nil || h.Get("Authorization") != "Bearer work-token" {
		t.Fatalf("rename changed bound credentials: %v %v", h, err)
	}
}

func TestAccountRefreshSerializedDurableAndRevoked(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 3600})
	}))
	defer srv.Close()
	originalURL := openAITokenURL
	openAITokenURL = srv.URL
	defer func() { openAITokenURL = originalURL }()

	account := Account{ID: "id", ProviderID: "chatgpt", Label: "L", OAuth: &OAuthCredentials{RefreshToken: "old-refresh", AccountID: "old-org", ExpiresAt: 1}}
	var persisted Account
	m := NewAccountManager(map[string]Account{"id": account}, func(a Account) error {
		persisted = a
		return nil
	})
	p, q := FromPreset("chatgpt", "", ""), FromPreset("chatgpt", "", "")
	for _, instance := range []*Provider{p, q} {
		if err := m.Bind(instance, "id"); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for _, instance := range []*Provider{p, q} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := instance.RequestHeaders(context.Background())
			if err != nil || h.Get("Authorization") != "Bearer new-access" || h.Get("chatgpt-account-id") != "old-org" {
				t.Errorf("headers %v: %v", h, err)
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 || persisted.OAuth.RefreshToken != "new-refresh" {
		t.Fatalf("refresh calls %d; durable rotation %+v", calls.Load(), persisted.OAuth)
	}
	if got, _ := m.Account("id"); got.OAuth.AccessToken != "new-access" || got.OAuth.RefreshToken != "new-refresh" {
		t.Fatalf("manager has stale token: %+v", got.OAuth)
	}
	m.Replace(map[string]Account{})
	if _, err := p.RequestHeaders(context.Background()); err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatalf("removed binding accepted: %v", err)
	}
	m.Replace(map[string]Account{"id": {ID: "id", ProviderID: "chatgpt", APIKey: "reconnected"}})
	if _, err := p.RequestHeaders(context.Background()); err == nil {
		t.Fatal("old binding accepted reconnected account")
	}
	if err := m.Bind(p, "id"); err != nil {
		t.Fatal(err)
	}
	h, err := p.RequestHeaders(context.Background())
	if err != nil || h.Get("Authorization") != "Bearer reconnected" || h.Get("chatgpt-account-id") != "" {
		t.Fatalf("reconnected account headers: %v, %v", h, err)
	}
}

func TestRefreshRemovalAndPersistenceFailureNeverPublishToken(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
			<-release
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "rotated", "refresh_token": "rotated-refresh", "expires_in": 3600})
	}))
	defer srv.Close()
	originalURL := openAITokenURL
	openAITokenURL = srv.URL
	defer func() { openAITokenURL = originalURL }()
	old := Account{ID: "id", ProviderID: "chatgpt", OAuth: &OAuthCredentials{RefreshToken: "old", ExpiresAt: 1}}
	var writes atomic.Int32
	m := NewAccountManager(map[string]Account{"id": old}, func(Account) error { writes.Add(1); return nil })
	p := FromPreset("chatgpt", "", "")
	if err := m.Bind(p, "id"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := p.RequestHeaders(context.Background()); result <- err }()
	<-started
	m.Replace(map[string]Account{"id": {ID: "id", ProviderID: "chatgpt", APIKey: "new-key"}})
	close(release)
	if err := <-result; err == nil {
		t.Fatal("stale refresh succeeded after reconnect")
	}
	if writes.Load() != 0 {
		t.Fatal("persisted stale refresh")
	}
	if got, _ := m.Account("id"); got.APIKey != "new-key" || got.OAuth != nil {
		t.Fatalf("stale credentials resurrected: %+v", got)
	}

	m = NewAccountManager(map[string]Account{"id": old}, func(Account) error { return errors.New("disk failure") })
	p = FromPreset("chatgpt", "", "")
	if err := m.Bind(p, "id"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.RequestHeaders(context.Background()); err == nil || !strings.Contains(err.Error(), "disk failure") {
		t.Fatalf("persistence failure not surfaced: %v", err)
	}
	if got, _ := m.Account("id"); got.OAuth.AccessToken != "" || got.OAuth.RefreshToken != "old" {
		t.Fatalf("failed rotation published: %+v", got.OAuth)
	}
}

func TestBlockedAccountRefreshDoesNotBlockAnotherAccount(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "rotated", "expires_in": 3600})
	}))
	defer srv.Close()
	originalURL := openAITokenURL
	openAITokenURL = srv.URL
	defer func() { openAITokenURL = originalURL }()
	m := NewAccountManager(map[string]Account{
		"slow": {ID: "slow", ProviderID: "chatgpt", OAuth: &OAuthCredentials{RefreshToken: "refresh", ExpiresAt: 1}},
		"fast": {ID: "fast", ProviderID: "chatgpt", APIKey: "fast-key"},
	}, func(Account) error { return nil })
	slow, fast := FromPreset("chatgpt", "", ""), FromPreset("chatgpt", "", "")
	if err := m.Bind(slow, "slow"); err != nil {
		t.Fatal(err)
	}
	if err := m.Bind(fast, "fast"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := slow.RequestHeaders(context.Background()); done <- err }()
	<-started
	h, err := fast.RequestHeaders(context.Background())
	close(release)
	if err != nil || h.Get("Authorization") != "Bearer fast-key" {
		t.Fatalf("blocked refresh stalled or changed independent account: %v %v", h, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("slow refresh: %v", err)
	}
}

func TestSetOAuthCopiesCredentialAndSessionIdentity(t *testing.T) {
	p := FromPreset("chatgpt", "", "")
	creds := &OAuthCredentials{AccessToken: "original", AccountID: "org", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	p.SetOAuth(creds)
	creds.AccessToken = "changed"
	copy := p.OAuth()
	copy.AccountID = "changed"
	h, err := p.RequestHeaders(context.Background())
	if err != nil || h.Get("Authorization") != "Bearer original" || h.Get("chatgpt-account-id") != "org" {
		t.Fatalf("aliased transient credentials: headers %v, err %v", h, err)
	}
}
