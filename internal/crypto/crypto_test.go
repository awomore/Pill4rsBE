package crypto

import (
	"testing"
)

func TestHashAndCheckPassword(t *testing.T) {
	tests := []struct {
		name          string
		password      string
		wrongPassword string
	}{
		{"simple ascii", "password123", "wrongpassword"},
		{"empty string", "", "x"},
		{"unicode", "пароль123", "wrong"},
		{"special chars", "p@$$w0rd!%", "different"},
		{"longish password", "a-password-of-exactly-seventy-two-bytes-long-which-is-the-max-limit", "nope"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, err := HashPassword(tt.password)
			if err != nil {
				t.Fatalf("HashPassword failed: %v", err)
			}
			if hash == "" {
				t.Fatal("expected non-empty hash")
			}
			if err := CheckPassword(hash, tt.password); err != nil {
				t.Fatalf("CheckPassword with correct password failed: %v", err)
			}
			if tt.wrongPassword != "" && tt.wrongPassword != tt.password {
				if err := CheckPassword(hash, tt.wrongPassword); err == nil {
					t.Fatal("CheckPassword with wrong password should have failed")
				}
			}
		})
	}
}

func TestCheckPasswordInvalidHash(t *testing.T) {
	if err := CheckPassword("not-a-valid-hash", "anything"); err == nil {
		t.Fatal("expected error for invalid hash")
	}
}

func TestSignAndVerifyAccessToken(t *testing.T) {
	secret := []byte("test-secret-key-32-bytes-long!")
	tests := []struct {
		name        string
		userID      string
		workspaceID string
	}{
		{"normal uuids", "550e8400-e29b-41d4-a716-446655440000", "660e8400-e29b-41d4-a716-446655440001"},
		{"empty strings", "", ""},
		{"numeric ids", "12345", "67890"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := SignAccessToken(tt.userID, tt.workspaceID, secret)
			if err != nil {
				t.Fatalf("SignAccessToken failed: %v", err)
			}
			if token == "" {
				t.Fatal("expected non-empty token")
			}

			claims, err := VerifyAccessToken(token, secret)
			if err != nil {
				t.Fatalf("VerifyAccessToken failed: %v", err)
			}
			if claims.UserID != tt.userID {
				t.Errorf("user_id: got %q, want %q", claims.UserID, tt.userID)
			}
			if claims.WorkspaceID != tt.workspaceID {
				t.Errorf("workspace_id: got %q, want %q", claims.WorkspaceID, tt.workspaceID)
			}
		})
	}
}

func TestVerifyInvalidToken(t *testing.T) {
	secret := []byte("test-secret-key-32-bytes-long!")

	validToken, err := SignAccessToken("user", "ws", []byte("a-different-secret"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		token   string
		secret  []byte
	}{
		{"garbage string", "not.a.valid.token", secret},
		{"empty token", "", secret},
		{"token signed with different secret", validToken, secret},
		{"wrong secret", func() string { t, _ := SignAccessToken("u", "w", secret); return t }(), []byte("wrong-secret")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := VerifyAccessToken(tt.token, tt.secret)
			if err == nil {
				t.Fatal("expected error for invalid token")
			}
		})
	}
}

func TestExpiredToken(t *testing.T) {
	secret := []byte("test-secret")
	token, _ := SignAccessToken("u", "w", secret)
	claims, _ := VerifyAccessToken(token, secret)
	if claims.ExpiresAt == nil {
		t.Fatal("expected expiry claim")
	}
	if claims.IssuedAt == nil {
		t.Fatal("expected issued-at claim")
	}
}
