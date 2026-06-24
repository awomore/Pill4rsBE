package crypto

import "testing"

func TestEncryptDecryptToken(t *testing.T) {
	tests := []struct {
		name      string
		plaintext string
		key       string
	}{
		{"short key", "EAAGm0PX...meta-access-token", "test-key"},
		{"exact 32 byte key", "another-token-value", "01234567890123456789012345678901"},
		{"long key", "tok", "this-is-a-very-long-encryption-key-far-beyond-32-bytes-in-length"},
		{"empty plaintext", "", "test-key"},
		{"unicode plaintext", "tøken-ünîcode-密钥", "test-key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := EncryptToken(tt.plaintext, tt.key)
			if err != nil {
				t.Fatalf("EncryptToken failed: %v", err)
			}
			if enc == tt.plaintext && tt.plaintext != "" {
				t.Fatal("ciphertext must not equal plaintext")
			}

			dec, err := DecryptToken(enc, tt.key)
			if err != nil {
				t.Fatalf("DecryptToken failed: %v", err)
			}
			if dec != tt.plaintext {
				t.Fatalf("round trip mismatch: got %q want %q", dec, tt.plaintext)
			}
		})
	}
}

func TestEncryptTokenProducesDifferentCiphertext(t *testing.T) {
	a, _ := EncryptToken("same-plaintext", "key")
	b, _ := EncryptToken("same-plaintext", "key")
	if a == b {
		t.Fatal("expected random nonce to produce distinct ciphertexts")
	}
}

func TestDecryptTokenWrongKey(t *testing.T) {
	enc, err := EncryptToken("secret-token", "correct-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptToken(enc, "wrong-key"); err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestDecryptTokenTampered(t *testing.T) {
	enc, err := EncryptToken("secret-token", "key")
	if err != nil {
		t.Fatal(err)
	}
	tampered := enc[:len(enc)-2] + "AA"
	if _, err := DecryptToken(tampered, "key"); err == nil {
		t.Fatal("expected error decrypting tampered ciphertext")
	}
}

func TestDecryptTokenInvalidInput(t *testing.T) {
	if _, err := DecryptToken("not-base64-!!!", "key"); err == nil {
		t.Fatal("expected error for non-base64 input")
	}
	if _, err := DecryptToken("c2hvcnQ=", "key"); err == nil {
		t.Fatal("expected error for ciphertext shorter than nonce")
	}
}
