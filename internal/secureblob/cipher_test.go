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
	testutil.NoError(t, err, "NewCipher: %v")
	testutil.False(t, !cipher.Available(), "cipher reported unavailable with a 32-byte key")
	sealed, err := cipher.Seal("gateway state")
	testutil.NoError(t, err, "Seal: %v")
	testutil.MustNotContain(t, sealed, "gateway state")
	plain, err := cipher.Open(sealed)
	testutil.Equal(t, err, nil)
	testutil.Equal(t, plain, "gateway state")
	// Two seals of the same value must differ: the nonce is random, so equal
	// blobs would mean a reused nonce.
	again, err := cipher.Seal("gateway state")
	testutil.NoError(t, err, "Seal: %v")
	testutil.NotEqual(t, again, sealed)
}

func TestCipherRejectsForeignAndTamperedBlobs(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	cipher, err := NewCipher(key)
	testutil.NoError(t, err, "NewCipher: %v")
	sealed, err := cipher.Seal("gateway state")
	testutil.NoError(t, err, "Seal: %v")

	other, err := NewCipher([]byte("fedcba9876543210fedcba9876543210"))
	testutil.NoError(t, err, "NewCipher: %v")
	_, err = other.Open(sealed)
	testutil.Error(t, err)
	_, err = cipher.Open("!!!!")
	testutil.Error(t, err)
	_, err = cipher.Open(sealed[:len(sealed)-2])
	testutil.Error(t, err)
	// Flip a byte of the decoded ciphertext, not of the base64 text: the last
	// base64 character carries unused bits, so editing it can decode to exactly
	// the same bytes and prove nothing.
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	testutil.NoError(t, err, "decode sealed: %v")
	raw[len(raw)-1] ^= 0x01
	_, err = cipher.Open(base64.RawURLEncoding.EncodeToString(raw))
	testutil.Error(t, err)
}

func TestEmptyKeyDisablesSealing(t *testing.T) {
	cipher, err := NewCipher(nil)
	testutil.NoError(t, err, "NewCipher(nil): %v")
	testutil.False(t, cipher != nil, "NewCipher(nil) returned a cipher")
	testutil.False(t, cipher.Available(), "nil cipher reported as available")
	_, err = cipher.Seal("x")
	testutil.Error(t, err)
	_, err = cipher.Open("x")
	testutil.Error(t, err)
}

// The derived key must be domain-separated from the credential cipher: the same
// root key under a different label has to produce an incompatible cipher.
func TestDerivedKeyIsDomainSeparated(t *testing.T) {
	root := []byte("0123456789abcdef0123456789abcdef")
	cipher, err := NewCipher(root)
	testutil.NoError(t, err, "NewCipher: %v")
	sealed, err := cipher.Seal("state")
	testutil.NoError(t, err, "Seal: %v")
	// The same root must also work across independently constructed instances,
	// as it does after a gateway restart.
	restarted, err := NewCipher(append([]byte(nil), root...))
	testutil.NoError(t, err)
	plain, err := restarted.Open(sealed)
	testutil.Falsef(t, err != nil || plain != "state", "same-root cipher after restart failed: plain=%q err=%v", plain, err)
	// A cipher built from the raw root key (what the credential path uses) must
	// not be able to open a sealed blob.
	block, err := aes.NewCipher(root)
	testutil.NoError(t, err)
	rawAEAD, err := cryptocipher.NewGCM(block)
	testutil.NoError(t, err)
	blob, err := base64.RawURLEncoding.DecodeString(sealed)
	testutil.NoError(t, err)
	testutil.False(t, len(blob) < rawAEAD.NonceSize(), "sealed blob has no nonce")
	_, err = rawAEAD.Open(nil, blob[:rawAEAD.NonceSize()], blob[rawAEAD.NonceSize():], nil)
	testutil.Error(t, err)
	// The opposite direction must be isolated too.
	nonce := make([]byte, rawAEAD.NonceSize())
	credentialBlob := rawAEAD.Seal(nonce, nonce, []byte("credential state"), nil)
	_, err = cipher.Open(base64.RawURLEncoding.EncodeToString(credentialBlob))
	testutil.Error(t, err)
}
