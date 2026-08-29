package auth

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKey(t)
	plaintext := []byte("JBSWY3DPEHPK3PXP")

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatal("szyfrogram zawiera tekst jawny")
	}

	got, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("Decrypt = %q, want %q", got, plaintext)
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	key := testKey(t)
	plaintext := []byte("JBSWY3DPEHPK3PXP")

	first, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	second, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if bytes.Equal(first, second) {
		t.Error("dwa szyfrowania tego samego sekretu dały identyczny wynik")
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	key := testKey(t)

	ciphertext, err := Encrypt(key, []byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	tampered := bytes.Clone(ciphertext)
	tampered[len(tampered)-1] ^= 0xFF

	if _, err := Decrypt(key, tampered); err == nil {
		t.Error("Decrypt przyjął zmodyfikowany szyfrogram")
	}
}

func TestDecryptRejectsWrongKey(t *testing.T) {
	ciphertext, err := Encrypt(testKey(t), []byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := Decrypt(testKey(t), ciphertext); err == nil {
		t.Error("Decrypt przyjął szyfrogram z innym kluczem")
	}
}

func TestDecryptRejectsTruncatedInput(t *testing.T) {
	if _, err := Decrypt(testKey(t), []byte{1, 2, 3}); err == nil {
		t.Error("Decrypt przyjął dane krótsze niż nonce")
	}
}

func TestCryptoRejectsWrongKeyLength(t *testing.T) {
	for _, size := range []int{0, 16, 31, 33} {
		key := make([]byte, size)
		if _, err := Encrypt(key, []byte("x")); err == nil {
			t.Errorf("Encrypt przyjął klucz o długości %d", size)
		}
	}
}
