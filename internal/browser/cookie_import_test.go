package browser

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"strings"
	"testing"

	"crypto/pbkdf2"

	"github.com/chromedp/cdproto/network"
)

// --- Test Helpers ---

// deriveTestKey derives an AES-128 key using the same PBKDF2 parameters as Chromium.
func deriveTestKey(t *testing.T, password string, iterations int) []byte {
	t.Helper()
	key, err := pbkdf2.Key(sha1.New, password, []byte("saltysalt"), iterations, 16)
	if err != nil {
		t.Fatalf("pbkdf2: %v", err)
	}
	return key
}

// encryptTestCookie encrypts a value using the same scheme as Chromium:
// prefix(3) + AES-128-CBC(HMAC_tag(32) + value, pad=PKCS7, iv=16×0x20)
func encryptTestCookie(t *testing.T, value string, key []byte, prefix string) []byte {
	t.Helper()

	hmacTag := make([]byte, 32)
	if _, err := rand.Read(hmacTag); err != nil {
		t.Fatalf("rand: %v", err)
	}
	plaintext := append(hmacTag, []byte(value)...)

	padLen := aes.BlockSize - (len(plaintext) % aes.BlockSize)
	padded := append(plaintext, bytes.Repeat([]byte{byte(padLen)}, padLen)...)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	iv := bytes.Repeat([]byte{0x20}, aes.BlockSize)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)

	return append([]byte(prefix), ct...)
}

// --- Tests ---

func TestCookieImport(t *testing.T) {
	// Linux v10 key: password="peanuts", salt="saltysalt", iterations=1, keyLen=16, sha1
	v10Key := deriveTestKey(t, "peanuts", 1)

	// --- AES-128-CBC decrypt round-trip ---

	t.Run("decrypt_round_trip", func(t *testing.T) {
		encrypted := encryptTestCookie(t, "hello-world", v10Key, "v10")
		keys := &decryptionKeys{v10Key: v10Key}

		got, err := decryptCookieValue(encrypted, keys)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if got != "hello-world" {
			t.Fatalf("decrypted = %q, want %q", got, "hello-world")
		}
	})

	t.Run("decrypt_round_trip_special_chars", func(t *testing.T) {
		value := "a=b&c=d; path=/; expires=Thu, 01 Jan 2099"
		encrypted := encryptTestCookie(t, value, v10Key, "v10")
		keys := &decryptionKeys{v10Key: v10Key}

		got, err := decryptCookieValue(encrypted, keys)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if got != value {
			t.Fatalf("decrypted = %q, want %q", got, value)
		}
	})

	t.Run("decrypt_empty_value_returns_empty", func(t *testing.T) {
		// Empty value: plaintext = 32-byte HMAC tag only → len ≤ 32 → ""
		encrypted := encryptTestCookie(t, "", v10Key, "v10")
		keys := &decryptionKeys{v10Key: v10Key}

		got, err := decryptCookieValue(encrypted, keys)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if got != "" {
			t.Fatalf("decrypted = %q, want empty string", got)
		}
	})

	t.Run("decrypt_v11_key", func(t *testing.T) {
		v11Key := deriveTestKey(t, "test-v11-secret", 1)
		encrypted := encryptTestCookie(t, "v11-value", v11Key, "v11")
		keys := &decryptionKeys{v10Key: v10Key, v11Key: v11Key}

		got, err := decryptCookieValue(encrypted, keys)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if got != "v11-value" {
			t.Fatalf("decrypted = %q, want %q", got, "v11-value")
		}
	})

	t.Run("decrypt_long_value", func(t *testing.T) {
		value := strings.Repeat("x", 500)
		encrypted := encryptTestCookie(t, value, v10Key, "v10")
		keys := &decryptionKeys{v10Key: v10Key}

		got, err := decryptCookieValue(encrypted, keys)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if got != value {
			t.Fatalf("decrypted length = %d, want %d", len(got), len(value))
		}
	})

	// --- PKCS7 padding/unpadding ---

	t.Run("pkcs7_valid_padding", func(t *testing.T) {
		data := []byte{1, 2, 3, 4, 5, 3, 3, 3}
		got, err := pkcs7Unpad(data, 8)
		if err != nil {
			t.Fatalf("unpad: %v", err)
		}
		if !bytes.Equal(got, []byte{1, 2, 3, 4, 5}) {
			t.Fatalf("got %v, want [1 2 3 4 5]", got)
		}
	})

	t.Run("pkcs7_full_block_padding", func(t *testing.T) {
		data := bytes.Repeat([]byte{16}, 16)
		got, err := pkcs7Unpad(data, 16)
		if err != nil {
			t.Fatalf("unpad: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected empty, got %v", got)
		}
	})

	t.Run("pkcs7_single_byte_padding", func(t *testing.T) {
		data := []byte{0xAA, 0xBB, 0xCC, 1}
		got, err := pkcs7Unpad(data, 4)
		if err != nil {
			t.Fatalf("unpad: %v", err)
		}
		if !bytes.Equal(got, []byte{0xAA, 0xBB, 0xCC}) {
			t.Fatalf("got %v, want [AA BB CC]", got)
		}
	})

	t.Run("pkcs7_invalid_zero_pad", func(t *testing.T) {
		_, err := pkcs7Unpad([]byte{1, 2, 3, 0}, 4)
		if err == nil {
			t.Fatal("expected error for zero pad byte")
		}
	})

	t.Run("pkcs7_invalid_pad_exceeds_data_len", func(t *testing.T) {
		_, err := pkcs7Unpad([]byte{1, 2, 3, 5}, 8) // pad=5 but only 4 bytes
		if err == nil {
			t.Fatal("expected error for pad > data length")
		}
	})

	t.Run("pkcs7_invalid_pad_exceeds_block_size", func(t *testing.T) {
		data := make([]byte, 32)
		data[31] = 17 // pad=17, block=16
		_, err := pkcs7Unpad(data, 16)
		if err == nil {
			t.Fatal("expected error for pad > block size")
		}
	})

	t.Run("pkcs7_inconsistent_pad_bytes", func(t *testing.T) {
		data := []byte{1, 2, 3, 4, 5, 2, 3, 3} // pad=3 but byte at -3 is 2 not 3
		_, err := pkcs7Unpad(data, 8)
		if err == nil {
			t.Fatal("expected error for inconsistent pad bytes")
		}
		if !strings.Contains(err.Error(), "padding byte at offset") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("pkcs7_empty_data", func(t *testing.T) {
		_, err := pkcs7Unpad(nil, 16)
		if err == nil {
			t.Fatal("expected error for nil data")
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- sameSite mapping ---

	t.Run("sameSite_0_None", func(t *testing.T) {
		if got := chromiumSameSite(0); got != network.CookieSameSiteNone {
			t.Fatalf("got %q, want %q", got, network.CookieSameSiteNone)
		}
	})

	t.Run("sameSite_1_Lax", func(t *testing.T) {
		if got := chromiumSameSite(1); got != network.CookieSameSiteLax {
			t.Fatalf("got %q, want %q", got, network.CookieSameSiteLax)
		}
	})

	t.Run("sameSite_2_Strict", func(t *testing.T) {
		if got := chromiumSameSite(2); got != network.CookieSameSiteStrict {
			t.Fatalf("got %q, want %q", got, network.CookieSameSiteStrict)
		}
	})

	t.Run("sameSite_negative1_unspecified", func(t *testing.T) {
		if got := chromiumSameSite(-1); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("sameSite_99_unspecified", func(t *testing.T) {
		if got := chromiumSameSite(99); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	// --- Chromium epoch ↔ Unix timestamp ---

	t.Run("chromium_epoch_known_date", func(t *testing.T) {
		// 2024-01-01T00:00:00Z = Unix 1704067200
		// Chromium microseconds = (1704067200 + 11644473600) × 1_000_000
		chromiumMicros := int64(1704067200+chromiumEpochDelta) * 1_000_000
		got := chromiumTimeToUnix(chromiumMicros)
		if got != 1704067200 {
			t.Fatalf("got %d, want %d", got, 1704067200)
		}
	})

	t.Run("chromium_epoch_zero_returns_zero", func(t *testing.T) {
		if got := chromiumTimeToUnix(0); got != 0 {
			t.Fatalf("got %d, want 0", got)
		}
	})

	t.Run("chromium_epoch_unix_epoch", func(t *testing.T) {
		// Unix epoch (1970-01-01) → Chromium micros = 11644473600 × 1_000_000
		chromiumMicros := int64(chromiumEpochDelta) * 1_000_000
		got := chromiumTimeToUnix(chromiumMicros)
		if got != 0 {
			t.Fatalf("got %d, want 0", got)
		}
	})

	// --- Decrypt error handling ---

	t.Run("decrypt_too_short", func(t *testing.T) {
		keys := &decryptionKeys{v10Key: v10Key}
		_, err := decryptCookieValue([]byte("v1"), keys)
		if err == nil {
			t.Fatal("expected error for too-short input")
		}
		if !strings.Contains(err.Error(), "too short") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("decrypt_unknown_prefix", func(t *testing.T) {
		keys := &decryptionKeys{v10Key: v10Key}
		data := append([]byte("v99"), make([]byte, 16)...)
		_, err := decryptCookieValue(data, keys)
		if err == nil {
			t.Fatal("expected error for unknown prefix")
		}
		if !strings.Contains(err.Error(), "unknown version prefix") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("decrypt_ciphertext_not_block_aligned", func(t *testing.T) {
		keys := &decryptionKeys{v10Key: v10Key}
		data := append([]byte("v10"), make([]byte, 7)...) // 7 not multiple of 16
		_, err := decryptCookieValue(data, keys)
		if err == nil {
			t.Fatal("expected error for non-aligned ciphertext")
		}
		if !strings.Contains(err.Error(), "invalid ciphertext length") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("decrypt_corrupt_ciphertext_bad_padding", func(t *testing.T) {
		keys := &decryptionKeys{v10Key: v10Key}
		// 32 bytes of zeros → decrypts to something with bad PKCS7 padding
		data := append([]byte("v10"), make([]byte, 32)...)
		_, err := decryptCookieValue(data, keys)
		if err == nil {
			t.Fatal("expected error for corrupt ciphertext")
		}
	})

	t.Run("decrypt_no_key_for_v11", func(t *testing.T) {
		keys := &decryptionKeys{v10Key: v10Key} // no v11Key
		data := append([]byte("v11"), make([]byte, 16)...)
		_, err := decryptCookieValue(data, keys)
		if err == nil {
			t.Fatal("expected error for missing v11 key")
		}
		if !strings.Contains(err.Error(), "no key available") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Unknown browser ---

	t.Run("unknown_browser_error", func(t *testing.T) {
		mgr := &Manager{}
		_, err := mgr.cmdCookieImportBrowser([]string{"firefox", "--domain", "example.com"})
		if err == nil {
			t.Fatal("expected error for unknown browser")
		}
		if !strings.Contains(err.Error(), "unknown browser") {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(err.Error(), "firefox") {
			t.Fatalf("error should mention browser name: %v", err)
		}
	})

	t.Run("browser_registry_has_expected_entries", func(t *testing.T) {
		for _, name := range []string{"chrome", "chromium", "brave", "edge"} {
			if _, ok := chromiumBrowsers[name]; !ok {
				t.Fatalf("browser %q not in registry", name)
			}
		}
	})

	// --- Command arg parsing ---

	t.Run("no_args_error", func(t *testing.T) {
		mgr := &Manager{}
		_, err := mgr.cmdCookieImportBrowser(nil)
		if err == nil {
			t.Fatal("expected error for no args")
		}
	})

	t.Run("missing_domain_error", func(t *testing.T) {
		mgr := &Manager{}
		_, err := mgr.cmdCookieImportBrowser([]string{"chrome"})
		if err == nil {
			t.Fatal("expected error for missing --domain")
		}
		if !strings.Contains(err.Error(), "--domain") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown_option_error", func(t *testing.T) {
		mgr := &Manager{}
		_, err := mgr.cmdCookieImportBrowser([]string{"chrome", "--domain", "example.com", "--bogus"})
		if err == nil {
			t.Fatal("expected error for unknown option")
		}
		if !strings.Contains(err.Error(), "unknown option") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Profile path traversal ---

	t.Run("profile_path_traversal_fails", func(t *testing.T) {
		b := chromiumBrowsers["chrome"]
		_, err := findCookieDB(b, "../../../etc")
		if err == nil {
			t.Fatal("expected error for path traversal profile")
		}
	})

	t.Run("profile_path_with_slashes_fails", func(t *testing.T) {
		b := chromiumBrowsers["chrome"]
		_, err := findCookieDB(b, "Default/../../etc")
		if err == nil {
			t.Fatal("expected error for profile with embedded slashes")
		}
	})

	// --- SQL string escaping ---

	t.Run("escape_sql_single_quotes", func(t *testing.T) {
		if got := escapeSQLString("test'value"); got != "test''value" {
			t.Fatalf("got %q, want %q", got, "test''value")
		}
	})

	t.Run("escape_sql_no_quotes", func(t *testing.T) {
		if got := escapeSQLString("normal"); got != "normal" {
			t.Fatalf("got %q, want %q", got, "normal")
		}
	})

	t.Run("escape_sql_multiple_quotes", func(t *testing.T) {
		if got := escapeSQLString("a'b'c"); got != "a''b''c" {
			t.Fatalf("got %q, want %q", got, "a''b''c")
		}
	})
}
