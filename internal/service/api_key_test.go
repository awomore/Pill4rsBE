package service

import (
	"strings"
	"testing"
)

func TestGenerateAPIKey(t *testing.T) {
	raw, prefix, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, apiKeyPrefix) {
		t.Errorf("raw key %q missing prefix", raw)
	}
	if !strings.HasPrefix(raw, prefix) {
		t.Errorf("prefix %q is not a prefix of %q", prefix, raw)
	}
	if hash != HashAPIKey(raw) {
		t.Errorf("hash mismatch")
	}
	if len(raw) < 40 {
		t.Errorf("key too short: %q", raw)
	}

	raw2, _, _, err := GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if raw == raw2 {
		t.Error("generated duplicate keys")
	}
}

func TestHashAPIKey(t *testing.T) {
	a := HashAPIKey("p4_live_test")
	b := HashAPIKey("p4_live_test")
	if a != b {
		t.Errorf("hash is not deterministic")
	}
	if len(a) != 64 {
		t.Errorf("sha256 hex length = %d", len(a))
	}
	if a == HashAPIKey("p4_live_other") {
		t.Error("different keys hashed the same")
	}
}

func TestNormalizeScopes(t *testing.T) {
	got, err := NormalizeScopes(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(DefaultScopes) {
		t.Errorf("default scopes = %v", got)
	}

	got, err = NormalizeScopes([]string{" Campaigns:Read ", "campaigns:read", "accounts:read"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "campaigns:read" || got[1] != "accounts:read" {
		t.Errorf("normalized = %v", got)
	}

	if _, err := NormalizeScopes([]string{"campaigns:delete"}); err == nil {
		t.Error("expected unknown scope error")
	}
}
