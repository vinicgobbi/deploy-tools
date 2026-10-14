// Package appconf reads and writes <app>/.deploy.conf, the per-app settings
// the CI forced command consults during a deploy. The file is written by root
// (deploy-tools app create/migrate/config) and is not writable by the deploy
// user, so a CI key cannot change how its own deploys behave (e.g. switch on
// migrations). It is parsed, never sourced/executed.
//
// Apps without the file keep the original behavior: extract, link shared/,
// swap current, prune — no hooks.
package appconf

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

const FileName = ".deploy.conf"

type Conf struct {
	Type           string // laravel | react | static
	LaravelMigrate bool   // run `php artisan migrate --force` on deploy
}

func Path(appDir string) string { return filepath.Join(appDir, FileName) }

// Read returns ok=false (and no error) when the app has no conf file.
func Read(appDir string) (c Conf, ok bool, err error) {
	f, err := os.Open(Path(appDir))
	if errors.Is(err, fs.ErrNotExist) {
		return Conf{}, false, nil
	}
	if err != nil {
		return Conf{}, false, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, found := strings.Cut(line, "=")
		if !found {
			return Conf{}, false, fmt.Errorf("%s:%d: expected KEY=value", FileName, n)
		}
		key, val = strings.TrimSpace(key), strings.Trim(strings.TrimSpace(val), `"'`)
		switch key {
		case "TYPE":
			if err := validate.Type(val); err != nil {
				return Conf{}, false, fmt.Errorf("%s:%d: %w", FileName, n, err)
			}
			c.Type = val
		case "LARAVEL_MIGRATE":
			switch val {
			case "yes":
				c.LaravelMigrate = true
			case "no", "":
				c.LaravelMigrate = false
			default:
				return Conf{}, false, fmt.Errorf("%s:%d: LARAVEL_MIGRATE must be yes or no", FileName, n)
			}
		default:
			return Conf{}, false, fmt.Errorf("%s:%d: unknown key %q", FileName, n, key)
		}
	}
	if err := sc.Err(); err != nil {
		return Conf{}, false, err
	}
	return c, true, nil
}

// Write replaces the conf atomically (temp file + rename), mode 0644.
func Write(appDir string, c Conf) error {
	if err := validate.Type(c.Type); err != nil {
		return err
	}
	migrate := "no"
	if c.LaravelMigrate {
		migrate = "yes"
	}
	body := "# Gerado por deploy-tools. Edite com: deploy-tools app config <app>\n" +
		"TYPE=" + c.Type + "\n" +
		"LARAVEL_MIGRATE=" + migrate + "\n"

	tmp := Path(appDir) + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, Path(appDir))
}

// DetectType guesses the app type from a release directory.
func DetectType(releaseDir string) string {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(releaseDir, name))
		return err == nil
	}
	switch {
	case exists("artisan"):
		return validate.Laravel
	case exists("index.html") && exists("assets"):
		return validate.React
	default:
		return validate.Static
	}
}
