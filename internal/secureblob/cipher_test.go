package secureblob

import (
	"crypto/aes"
	cryptocipher "crypto/cipher"
	"encoding/base64"
	"orchids-api/internal/testutil"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	cipher, err := NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	if !cipher.Available() {
		t.Fatal("cipher reported unavailable with a 32-byte key")
	}
	sealed, err := cipher.Seal("gateway state")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	testutil.MustNotContain(t, sealed, "gateway state")
	plain, err := cipher.Open(sealed)
	if err != nil || plain != "gateway state" {
		t.Fatalf("Open=%q err=%v", plain, err)
	}
	// Two seals of the same value must differ: the nonce is random, so equal
	// blobs would mean a reused nonce.
	again, err := cipher.Seal("gateway state")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	testutil.NotEqual(t, again, sealed)
}

func TestCipherRejectsForeignAndTamperedBlobs(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	cipher, err := NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	sealed, err := cipher.Seal("gateway state")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	other, err := NewCipher([]byte("fedcba9876543210fedcba9876543210"))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("a blob sealed with one key opened with another")
	}
	if _, err := cipher.Open("!!!!"); err == nil {
		t.Fatal("malformed base64 was accepted")
	}
	if _, err := cipher.Open(sealed[:len(sealed)-2]); err == nil {
		t.Fatal("truncated blob was accepted")
	}
	// Flip a byte of the decoded ciphertext, not of the base64 text: the last
	// base64 character carries unused bits, so editing it can decode to exactly
	// the same bytes and prove nothing.
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatalf("decode sealed: %v", err)
	}
	raw[len(raw)-1] ^= 0x01
	if _, err := cipher.Open(base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("tampered blob was accepted")
	}
}

func TestEmptyKeyDisablesSealing(t *testing.T) {
	cipher, err := NewCipher(nil)
	if err != nil {
		t.Fatalf("NewCipher(nil): %v", err)
	}
	if cipher != nil {
		t.Fatal("NewCipher(nil) returned a cipher")
	}
	if cipher.Available() {
		t.Fatal("nil cipher reported as available")
	}
	if _, err := cipher.Seal("x"); err == nil {
		t.Fatal("nil cipher sealed a value")
	}
	if _, err := cipher.Open("x"); err == nil {
		t.Fatal("nil cipher opened a value")
	}
}

// The derived key must be domain-separated from the credential cipher: the same
// root key under a different label has to produce an incompatible cipher.
func TestDerivedKeyIsDomainSeparated(t *testing.T) {
	root := []byte("0123456789abcdef0123456789abcdef")
	cipher, err := NewCipher(root)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	sealed, err := cipher.Seal("state")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// The same root must also work across independently constructed instances,
	// as it does after a gateway restart.
	restarted, err := NewCipher(append([]byte(nil), root...))
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := restarted.Open(sealed); err != nil || plain != "state" {
		t.Fatalf("same-root cipher after restart failed: plain=%q err=%v", plain, err)
	}
	// A cipher built from the raw root key (what the credential path uses) must
	// not be able to open a sealed blob.
	block, err := aes.NewCipher(root)
	if err != nil {
		t.Fatal(err)
	}
	rawAEAD, err := cryptocipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if len(blob) < rawAEAD.NonceSize() {
		t.Fatal("sealed blob has no nonce")
	}
	if _, err := rawAEAD.Open(nil, blob[:rawAEAD.NonceSize()], blob[rawAEAD.NonceSize():], nil); err == nil {
		t.Fatal("raw credential key opened a domain-separated secure blob")
	}
	// The opposite direction must be isolated too.
	nonce := make([]byte, rawAEAD.NonceSize())
	credentialBlob := rawAEAD.Seal(nonce, nonce, []byte("credential state"), nil)
	if _, err := cipher.Open(base64.RawURLEncoding.EncodeToString(credentialBlob)); err == nil {
		t.Fatal("secure blob cipher opened a raw credential-key ciphertext")
	}
}
