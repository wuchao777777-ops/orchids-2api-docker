package responses

import (
	"testing"

	"orchids-api/internal/secureblob"
	"orchids-api/internal/testutil"
)

func testCompactionCipher(t *testing.T) *secureblob.Cipher {
	t.Helper()
	cipher, err := secureblob.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	testutil.NoError(t, err, "secureblob.NewCipher: %v")
	testutil.False(t, !cipher.Available(), "cipher is not available")
	return cipher
}
