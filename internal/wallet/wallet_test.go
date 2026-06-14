package wallet

import (
	"bytes"
	"testing"
)

func TestGenerateAccount(t *testing.T) {
	acct, err := GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	if len(acct.PrivateKey) != 32 {
		t.Errorf("private key length = %d, want 32", len(acct.PrivateKey))
	}
	if norm, ok := NormalizeAddress(acct.Address); !ok || norm != acct.Address {
		t.Errorf("generated address %q is not a valid checksummed address (ok=%v norm=%q)", acct.Address, ok, norm)
	}

	// Two generated accounts must differ.
	other, err := GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount(2): %v", err)
	}
	if acct.Address == other.Address {
		t.Error("two generated accounts share the same address")
	}
}

func TestNormalizeAddress(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"0x52908400098527886e0f7030069857d2e4169ee7", "0x52908400098527886E0F7030069857D2E4169EE7", true},
		{"0x52908400098527886E0F7030069857D2E4169EE7", "0x52908400098527886E0F7030069857D2E4169EE7", true},
		{"0x123", "", false},
		{"not-an-address", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeAddress(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeAddress(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestDeriveKey(t *testing.T) {
	k := DeriveKey([]byte("some-passphrase"))
	if len(k) != 32 {
		t.Errorf("DeriveKey length = %d, want 32", len(k))
	}
	// Deterministic.
	if !bytes.Equal(k, DeriveKey([]byte("some-passphrase"))) {
		t.Error("DeriveKey is not deterministic")
	}
	if bytes.Equal(k, DeriveKey([]byte("other"))) {
		t.Error("DeriveKey collided for different passphrases")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := DeriveKey([]byte("pass"))
	plain := []byte("super-secret-private-key-bytes")

	ct, err := Encrypt(plain, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(ct, plain) {
		t.Error("ciphertext equals plaintext")
	}

	got, err := Decrypt(ct, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("round trip = %q, want %q", got, plain)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	ct, err := Encrypt([]byte("data"), DeriveKey([]byte("right")))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt(ct, DeriveKey([]byte("wrong"))); err == nil {
		t.Error("Decrypt with wrong key should fail")
	}
}

func TestDecryptShortCiphertext(t *testing.T) {
	if _, err := Decrypt([]byte("x"), DeriveKey([]byte("k"))); err == nil {
		t.Error("Decrypt of too-short ciphertext should fail")
	}
}
