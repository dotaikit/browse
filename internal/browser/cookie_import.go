package browser

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// chromiumBrowser holds platform-specific paths and keychain info for a Chromium-based browser.
type chromiumBrowser struct {
	macDir          string // relative to ~/Library/Application Support/
	linuxDir        string // relative to ~/.config/
	keychainService string // macOS keychain service name
	linuxApp        string // application name for secret-tool lookup
}

var chromiumBrowsers = map[string]chromiumBrowser{
	"chrome":   {"Google/Chrome", "google-chrome", "Chrome Safe Storage", "chrome"},
	"chromium": {"chromium", "chromium", "Chromium Safe Storage", "chromium"},
	"brave":    {"BraveSoftware/Brave-Browser", "BraveSoftware/Brave-Browser", "Brave Safe Storage", "brave"},
	"edge":     {"Microsoft Edge", "microsoft-edge", "Microsoft Edge Safe Storage", "microsoft-edge"},
}

// decryptionKeys holds derived AES keys for v10 and v11 cookie encryption.
type decryptionKeys struct {
	v10Key []byte
	v11Key []byte // Linux only; nil on macOS
}

// sqliteCookieRow represents a row from the sqlite3 JSON output.
type sqliteCookieRow struct {
	HostKey           string `json:"host_key"`
	Name              string `json:"name"`
	Value             string `json:"value"`
	EncryptedValueHex string `json:"encrypted_value_hex"`
	Path              string `json:"path"`
	ExpiresUTC        int64  `json:"expires_utc"`
	IsSecure          int    `json:"is_secure"`
	IsHTTPOnly        int    `json:"is_httponly"`
	SameSite          int    `json:"samesite"`
}

// cmdCookieImportBrowser imports cookies from a local Chromium-based browser profile.
// Usage: cookie-import-browser <browser> --domain <domain> [--profile <profile>]
func (m *Manager) cmdCookieImportBrowser(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: cookie-import-browser <browser> --domain <domain> [--profile <profile>]\nbrowsers: chrome, chromium, brave, edge")
	}

	browserName := strings.ToLower(args[0])
	var domain, profile string
	profile = "Default"

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--domain":
			if i+1 >= len(args) {
				return "", fmt.Errorf("--domain requires a value")
			}
			i++
			domain = args[i]
		case "--profile":
			if i+1 >= len(args) {
				return "", fmt.Errorf("--profile requires a value")
			}
			i++
			profile = args[i]
		default:
			return "", fmt.Errorf("unknown option: %s", args[i])
		}
	}

	if domain == "" {
		return "", fmt.Errorf("--domain is required")
	}

	browser, ok := chromiumBrowsers[browserName]
	if !ok {
		names := make([]string, 0, len(chromiumBrowsers))
		for k := range chromiumBrowsers {
			names = append(names, k)
		}
		return "", fmt.Errorf("unknown browser: %s (supported: %s)", browserName, strings.Join(names, ", "))
	}

	if _, err := exec.LookPath("sqlite3"); err != nil {
		return "", fmt.Errorf("sqlite3 not found in PATH (required for cookie decryption)")
	}

	// Locate and copy DB to avoid locking the browser's file
	dbPath, err := findCookieDB(browser, profile)
	if err != nil {
		return "", err
	}

	tmpPath, cleanup, err := copyCookieDB(dbPath)
	if err != nil {
		return "", err
	}
	defer cleanup()

	// Query cookies for the target domain
	rows, err := queryCookieDB(tmpPath, domain)
	if err != nil {
		return "", err
	}

	if len(rows) == 0 {
		return fmt.Sprintf("No cookies found for %s in %s (profile: %s)", domain, browserName, profile), nil
	}

	// Derive decryption keys
	keys, err := deriveCookieKeys(browser)
	if err != nil {
		return "", fmt.Errorf("derive decryption key: %w", err)
	}

	// Decrypt and import via CDP
	imported := 0
	var importErrors []string

	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		for _, row := range rows {
			value := row.Value
			if value == "" && row.EncryptedValueHex != "" {
				encBytes, err := hex.DecodeString(row.EncryptedValueHex)
				if err != nil {
					importErrors = append(importErrors, fmt.Sprintf("%s: bad hex: %v", row.Name, err))
					continue
				}
				decrypted, err := decryptCookieValue(encBytes, keys)
				if err != nil {
					importErrors = append(importErrors, fmt.Sprintf("%s: decrypt: %v", row.Name, err))
					continue
				}
				value = decrypted
			}
			if value == "" {
				continue
			}

			p := network.SetCookie(row.Name, value).
				WithDomain(row.HostKey).
				WithPath(row.Path).
				WithSecure(row.IsSecure != 0).
				WithHTTPOnly(row.IsHTTPOnly != 0)

			if ss := chromiumSameSite(row.SameSite); ss != "" {
				p = p.WithSameSite(ss)
			}

			if row.ExpiresUTC > 0 {
				unixSec := chromiumTimeToUnix(row.ExpiresUTC)
				if unixSec > 0 {
					t := cdp.TimeSinceEpoch(time.Unix(unixSec, 0))
					p = p.WithExpires(&t)
				}
			}

			if err := p.Do(ctx); err != nil {
				importErrors = append(importErrors, fmt.Sprintf("%s: set: %v", row.Name, err))
				continue
			}
			imported++
		}
		return nil
	})); err != nil {
		return "", fmt.Errorf("cookie-import-browser: %w", err)
	}

	result := fmt.Sprintf("Imported %d/%d cookies from %s for %s", imported, len(rows), browserName, domain)
	if len(importErrors) > 0 {
		result += fmt.Sprintf("\nErrors (%d): %s", len(importErrors), strings.Join(importErrors, "; "))
	}
	return result, nil
}

// --- DB discovery and access ---

func validateProfile(profile string) error {
	if strings.Contains(profile, "..") {
		return fmt.Errorf("invalid profile name: contains '..'")
	}
	if strings.Contains(profile, "/") || strings.Contains(profile, "\\") {
		return fmt.Errorf("invalid profile name: contains path separator")
	}
	for _, r := range profile {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid profile name: contains control character")
		}
	}
	return nil
}

func findCookieDB(b chromiumBrowser, profile string) (string, error) {
	if err := validateProfile(profile); err != nil {
		return "", err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}

	var base string
	switch runtime.GOOS {
	case "darwin":
		base = filepath.Join(home, "Library", "Application Support", b.macDir)
	case "linux":
		base = filepath.Join(home, ".config", b.linuxDir)
	default:
		return "", fmt.Errorf("unsupported OS: %s (supported: linux, darwin)", runtime.GOOS)
	}

	dbPath := filepath.Join(base, profile, "Cookies")
	if _, err := os.Stat(dbPath); err != nil {
		return "", fmt.Errorf("cookie database not found: %s", dbPath)
	}
	return dbPath, nil
}

func copyCookieDB(dbPath string) (tmpPath string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "browse-cookies-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	cleanupFn := func() { os.RemoveAll(tmpDir) }

	dir := filepath.Dir(dbPath)
	base := filepath.Base(dbPath)

	for _, suffix := range []string{"", "-wal", "-shm"} {
		src := filepath.Join(dir, base+suffix)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(tmpDir, base+suffix)
		if err := copyFileContents(src, dst); err != nil {
			cleanupFn()
			return "", nil, fmt.Errorf("copy %s: %w", filepath.Base(src), err)
		}
	}

	return filepath.Join(tmpDir, base), cleanupFn, nil
}

func copyFileContents(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func queryCookieDB(dbPath, domain string) ([]sqliteCookieRow, error) {
	cleanDomain := strings.TrimPrefix(domain, ".")
	dotDomain := "." + cleanDomain

	query := fmt.Sprintf(
		`SELECT host_key, name, value, hex(encrypted_value) as encrypted_value_hex, path, expires_utc, is_secure, is_httponly, samesite FROM cookies WHERE host_key = '%s' OR host_key = '%s'`,
		escapeSQLString(cleanDomain), escapeSQLString(dotDomain),
	)

	cmd := exec.Command("sqlite3", "-json", dbPath, query)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("sqlite3 query: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("sqlite3 query: %w", err)
	}

	out = []byte(strings.TrimSpace(string(out)))
	if len(out) == 0 {
		return nil, nil
	}

	var rows []sqliteCookieRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("parse sqlite3 output: %w", err)
	}
	return rows, nil
}

func escapeSQLString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// --- Key derivation ---

func deriveCookieKeys(b chromiumBrowser) (*decryptionKeys, error) {
	keys := &decryptionKeys{}
	salt := []byte("saltysalt")

	switch runtime.GOOS {
	case "darwin":
		password, err := macOSKeychainPassword(b.keychainService)
		if err != nil {
			return nil, fmt.Errorf("macOS keychain: %w", err)
		}
		key, err := pbkdf2.Key(sha1.New, password, salt, 1003, 16)
		if err != nil {
			return nil, fmt.Errorf("pbkdf2: %w", err)
		}
		keys.v10Key = key

	case "linux":
		// v10: hardcoded "peanuts" fallback
		v10, err := pbkdf2.Key(sha1.New, "peanuts", salt, 1, 16)
		if err != nil {
			return nil, fmt.Errorf("pbkdf2 v10: %w", err)
		}
		keys.v10Key = v10

		// v11: try secret-tool for GNOME Keyring / KWallet
		if password, err := linuxSecretToolPassword(b); err == nil && password != "" {
			v11, err := pbkdf2.Key(sha1.New, password, salt, 1, 16)
			if err != nil {
				return nil, fmt.Errorf("pbkdf2 v11: %w", err)
			}
			keys.v11Key = v11
		}

	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	return keys, nil
}

func macOSKeychainPassword(service string) (string, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", service, "-w")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("security find-generic-password -s %q: %w", service, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func linuxSecretToolPassword(b chromiumBrowser) (string, error) {
	attempts := [][]string{
		{"secret-tool", "lookup", "Title", b.keychainService},
		{"secret-tool", "lookup", "xdg:schema", "chrome_libsecret_os_crypt_password_v2", "application", b.linuxApp},
		{"secret-tool", "lookup", "xdg:schema", "chrome_libsecret_os_crypt_password", "application", b.linuxApp},
	}

	for _, argv := range attempts {
		cmd := exec.Command(argv[0], argv[1:]...)
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if pw := strings.TrimSpace(string(out)); pw != "" {
			return pw, nil
		}
	}

	return "", fmt.Errorf("secret-tool: no password found for %s", b.keychainService)
}

// --- Decryption ---

func decryptCookieValue(encrypted []byte, keys *decryptionKeys) (string, error) {
	if len(encrypted) < 4 { // 3-byte prefix + at least 1 block
		return "", fmt.Errorf("encrypted value too short (%d bytes)", len(encrypted))
	}

	prefix := string(encrypted[:3])
	ciphertext := encrypted[3:]

	var key []byte
	switch prefix {
	case "v10":
		key = keys.v10Key
	case "v11":
		key = keys.v11Key
	default:
		return "", fmt.Errorf("unknown version prefix: %q", prefix)
	}

	if key == nil {
		return "", fmt.Errorf("no key available for %s", prefix)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	if len(ciphertext) < aes.BlockSize || len(ciphertext)%aes.BlockSize != 0 {
		return "", fmt.Errorf("invalid ciphertext length %d (need multiple of %d)", len(ciphertext), aes.BlockSize)
	}

	// IV: 16 bytes of 0x20 (space)
	iv := make([]byte, aes.BlockSize)
	for i := range iv {
		iv[i] = 0x20
	}

	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)

	plaintext, err = pkcs7Unpad(plaintext, aes.BlockSize)
	if err != nil {
		return "", err
	}

	// Chromium prefixes decrypted cookie payloads with 32 bytes of metadata.
	if len(plaintext) <= 32 {
		return "", nil
	}
	return string(plaintext[32:]), nil
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > blockSize || padLen > len(data) {
		return nil, fmt.Errorf("invalid PKCS7 padding (%d)", padLen)
	}
	for i := len(data) - padLen; i < len(data); i++ {
		if data[i] != byte(padLen) {
			return nil, fmt.Errorf("invalid PKCS7 padding byte at offset %d", i)
		}
	}
	return data[:len(data)-padLen], nil
}

// --- Helpers ---

const chromiumEpochDelta = 11644473600 // seconds between 1601-01-01 and 1970-01-01

func chromiumTimeToUnix(chromiumMicros int64) int64 {
	if chromiumMicros == 0 {
		return 0
	}
	return chromiumMicros/1000000 - chromiumEpochDelta
}

func chromiumSameSite(val int) network.CookieSameSite {
	switch val {
	case 0:
		return network.CookieSameSiteNone
	case 1:
		return network.CookieSameSiteLax
	case 2:
		return network.CookieSameSiteStrict
	default:
		return "" // unspecified (-1 or unknown)
	}
}
