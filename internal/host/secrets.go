package host

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sauercrowd/gci/internal/config"
)

const secretRoot = stateRoot + "/secrets"
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

func secretDigest(values map[string]string) string {
	h := sha256.New()
	for _, env := range sortedKeys(values) {
		hashString(h, env)
		hashString(h, values[env])
	}
	return hex.EncodeToString(h.Sum(nil))
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
