package microsoft

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTokenCacheRoundTrip(t *testing.T) {
	t.Parallel()

	cache, err := newTokenCache(t.TempDir(), "mj41", DefaultClientID)
	if err != nil {
		t.Fatalf("newTokenCache: %v", err)
	}

	state := cachedLiveState{
		ClientID:   DefaultClientID,
		ObtainedAt: time.Now().UTC(),
		Token: liveTokenResponse{
			AccessToken:  "access",
			RefreshToken: "refresh",
			ExpiresIn:    3600,
		},
		Profile: &Profile{ID: "abc", Name: "mj41"},
	}
	if err := cache.save(state); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := cache.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded == nil {
		t.Fatal("load returned nil state")
	}
	if loaded.Token.RefreshToken != "refresh" {
		t.Fatalf("unexpected refresh token: %q", loaded.Token.RefreshToken)
	}
	if loaded.Profile == nil || loaded.Profile.Name != "mj41" {
		t.Fatalf("unexpected profile: %+v", loaded.Profile)
	}
	if filepath.Ext(cache.path) != ".json" {
		t.Fatalf("expected json cache file, got %s", cache.path)
	}
}

func TestLiveTokenStillValid(t *testing.T) {
	t.Parallel()

	valid := &cachedLiveState{
		ObtainedAt: time.Now().Add(-5 * time.Minute),
		Token:      liveTokenResponse{AccessToken: "ok", ExpiresIn: 3600},
	}
	if !liveTokenStillValid(valid) {
		t.Fatal("expected cached token to be valid")
	}

	expired := &cachedLiveState{
		ObtainedAt: time.Now().Add(-2 * time.Hour),
		Token:      liveTokenResponse{AccessToken: "old", ExpiresIn: 3600},
	}
	if liveTokenStillValid(expired) {
		t.Fatal("expected cached token to be expired")
	}
}
