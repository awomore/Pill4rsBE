package handler

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestVerifyFlutterwaveHash(t *testing.T) {
	secret := "flw_verif_hash"
	if !verifyFlutterwaveHash(secret, secret) {
		t.Fatal("expected matching hash to verify")
	}
	if verifyFlutterwaveHash("wrong", secret) {
		t.Fatal("expected mismatched hash to fail")
	}
	if verifyFlutterwaveHash("", secret) {
		t.Fatal("expected empty header to fail")
	}
	if verifyFlutterwaveHash(secret, "") {
		t.Fatal("expected empty secret to fail")
	}
}

func TestParseFlutterwaveMeta(t *testing.T) {
	obj := parseFlutterwaveMeta(json.RawMessage(`{"workspace_id":"abc","currency":"NGN"}`))
	if obj["workspace_id"] != "abc" || obj["currency"] != "NGN" {
		t.Fatalf("object meta parse = %+v", obj)
	}

	arr := parseFlutterwaveMeta(json.RawMessage(`[{"metaname":"workspace_id","metavalue":"abc"},{"metaname":"currency","metavalue":"USD"}]`))
	if arr["workspace_id"] != "abc" || arr["currency"] != "USD" {
		t.Fatalf("array meta parse = %+v", arr)
	}

	if got := parseFlutterwaveMeta(nil); len(got) != 0 {
		t.Fatalf("nil meta = %+v, want empty", got)
	}
}

func TestWorkspaceFromMeta(t *testing.T) {
	id := uuid.New()
	got, err := workspaceFromMeta(map[string]string{"workspace_id": id.String()})
	if err != nil || got != id {
		t.Fatalf("workspaceFromMeta = %v, %v; want %v", got, err, id)
	}
	if _, err := workspaceFromMeta(map[string]string{"workspace_id": "not-a-uuid"}); err == nil {
		t.Fatal("expected invalid workspace id to error")
	}
}
