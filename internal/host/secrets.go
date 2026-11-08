package host

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sauercrowd/gci/internal/config"
)

var secretRoot = stateRoot + "/secrets"

const secretDigestKeyName = ".digest-key"
const maxSecretSize = 64 * 1024

func secretPath(app, name string) string {
	return filepath.Join(secretRoot, app, name)
}

func SetSecret(app, name string, value []byte) error {
	if err := config.ValidateAppName(app); err != nil {
		return err
	}
	if err := config.ValidateSecretName(name); err != nil {
		return err
	}
	if app == "" || filepath.Base(app) != app {
		return fmt.Errorf("invalid app name")
	}
	if len(value) > maxSecretSize {
		return fmt.Errorf("secret exceeds 64 KiB")
	}
	dir := filepath.Join(secretRoot, app)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(secretRoot, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	return writeFileAtomic(secretPath(app, name), value, 0o600)
}

func ListSecrets(app string) ([]string, error) {
	if err := config.ValidateAppName(app); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(secretRoot, app))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			if err := config.ValidateSecretName(entry.Name()); err == nil {
				names = append(names, entry.Name())
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func RemoveSecret(app, name string) error {
	if err := config.ValidateAppName(app); err != nil {
		return err
	}
	if err := config.ValidateSecretName(name); err != nil {
		return err
	}
	if app == "" || filepath.Base(app) != app {
		return fmt.Errorf("invalid app name")
	}
	return os.Remove(secretPath(app, name))
}

func resolveSecrets(app string, svc *config.Service) (map[string]string, error) {
	if err := config.ValidateAppName(app); err != nil {
		return nil, err
	}
	values := make(map[string]string, len(svc.Secrets))
	var missing []string
	for _, env := range sortedKeys(svc.Secrets) {
		name := svc.Secrets[env]
		value, err := os.ReadFile(secretPath(app, name))
		if os.IsNotExist(err) {
			missing = append(missing, name)
			continue
		}
		if err != nil {
			return nil, err
		}
		values[env] = string(value)
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("missing secrets: %s", strings.Join(uniqueStrings(missing), ", "))
	}
	return values, nil
}

func secretDigest(values map[string]string) (string, error) {
	if len(values) == 0 {
		sum := sha256.Sum256(nil)
		return hex.EncodeToString(sum[:]), nil
	}
	key, err := secretDigestKey()
	if err != nil {
		return "", err
	}
	h := hmac.New(sha256.New, key)
	for _, env := range sortedKeys(values) {
		hashString(h, env)
		hashString(h, values[env])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func secretDigestKey() ([]byte, error) {
	if err := os.MkdirAll(secretRoot, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(secretRoot, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(secretRoot, secretDigestKeyName)
	key, err := readSecretDigestKey(path)
	if err == nil {
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	key = make([]byte, sha256.Size)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(secretRoot, ".digest-key-*")
	if err != nil {
		return nil, err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	n, writeErr := file.Write(key)
	if writeErr == nil && n != len(key) {
		writeErr = fmt.Errorf("short write creating secret digest key")
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := os.Link(tempPath, path); err != nil {
		if os.IsExist(err) {
			return readSecretDigestKey(path)
		}
		return nil, err
	}
	return key, nil
}

func readSecretDigestKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("secret digest key must be a regular file")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != sha256.Size {
		return nil, fmt.Errorf("secret digest key must be %d bytes", sha256.Size)
	}
	return key, nil
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
