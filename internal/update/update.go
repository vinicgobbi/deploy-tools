// Package update finds a newer release of deploy-tools on GitHub and
// downloads one of its binaries, checked against the .sha256 published
// next to it. It never replaces anything itself: callers install the
// downloaded file the way each binary installs itself.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LatestURL is the GitHub API endpoint of the latest release.
const LatestURL = "https://api.github.com/repos/vinicgobbi/deploy-tools/releases/latest"

// Release is a published version and its downloadable files.
type Release struct {
	Version string            // "v1.2.3"
	Assets  map[string]string // file name -> download URL
}

// Client talks to GitHub; the fields have working defaults.
type Client struct {
	HTTP *http.Client
	URL  string // LatestURL when empty
	// ChangelogURL is a format with the tag (%s); ChangelogURL when both
	// it and URL are empty (see Changes).
	ChangelogURL string
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

// Latest returns the latest release.
func (c Client) Latest(ctx context.Context) (Release, error) {
	url := c.URL
	if url == "" {
		url = LatestURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http().Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub respondeu %s ao consultar a última versão", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("resposta inesperada do GitHub: %w", err)
	}
	if _, ok := parse(body.TagName); !ok {
		return Release{}, fmt.Errorf("versão inesperada na última release: %q", body.TagName)
	}
	r := Release{Version: body.TagName, Assets: map[string]string{}}
	for _, a := range body.Assets {
		r.Assets[a.Name] = a.URL
	}
	return r, nil
}

// AssetName is the release file of binary for goos/goarch, as `make dist`
// names it (deploy-tools-linux-amd64, deploy-dev-windows-amd64.exe...).
func AssetName(binary, goos, goarch string) string {
	name := binary + "-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Download saves asset of r to a new executable file in dir and returns its
// path. The content must match the asset's .sha256, or nothing is kept.
func (c Client) Download(ctx context.Context, r Release, asset, dir string) (string, error) {
	url, ok := r.Assets[asset]
	if !ok {
		return "", fmt.Errorf("a release %s não tem %s", r.Version, asset)
	}
	sumURL, ok := r.Assets[asset+".sha256"]
	if !ok {
		return "", fmt.Errorf("a release %s não tem %s.sha256 para conferir o download", r.Version, asset)
	}
	sumText, err := c.get(ctx, sumURL, 4096)
	if err != nil {
		return "", err
	}
	want := strings.ToLower(strings.Fields(string(sumText) + " ")[0])
	if len(want) != 64 {
		return "", fmt.Errorf("%s.sha256 inválido", asset)
	}

	f, err := os.CreateTemp(dir, "."+asset+".*.download")
	if err != nil {
		return "", err
	}
	path := f.Name()
	ok = false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(path)
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download de %s: %s", asset, resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 256<<20)); err != nil {
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("o SHA-256 de %s não confere com o publicado (%s, esperado %s) — download descartado", asset, got[:12], want[:12])
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

func (c Client) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download de %s: %s", filepath.Base(url), resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// Compare compares "vX.Y.Z[-anything]"; ok=false when either is not a
// version (e.g. "dev").
func Compare(a, b string) (cmp int, ok bool) {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka || !okb {
		return 0, false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// Updatable reports whether a build may update itself: only release builds
// ("v1.2.3"), never "dev" nor git-describe builds ("v1.2.3-4-gabc",
// "-dirty").
func Updatable(version string) bool {
	_, ok := parse(version)
	return ok && !strings.ContainsAny(version, "-+")
}

// Newer reports whether latest is a newer release than current.
func Newer(current, latest string) bool {
	c, ok := Compare(latest, current)
	return ok && c > 0
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	if !strings.HasPrefix(v, "v") {
		return out, false
	}
	v = v[1:]
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Due reports whether the last automatic check, recorded in stamp, is older
// than every (or never happened).
func Due(stamp string, every time.Duration, now time.Time) bool {
	fi, err := os.Stat(stamp)
	return err != nil || now.Sub(fi.ModTime()) >= every
}

// Touch records an automatic check in stamp.
func Touch(stamp string, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(stamp), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(stamp, nil, 0o644); err != nil {
		return err
	}
	return os.Chtimes(stamp, now, now)
}

// ErrNoUpdate is returned when the running version is already the latest.
var ErrNoUpdate = errors.New("já está na versão mais recente")
