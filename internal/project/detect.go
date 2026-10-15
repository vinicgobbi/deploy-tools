package project

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	Laravel = "laravel"
	Symfony = "symfony"
	React   = "react"
	Angular = "angular"
	Static  = "static"
)

// Options is everything the deploy.yml template needs. Detect fills in
// defaults from the checkout; the CLI lets the developer adjust them.
type Options struct {
	App        string
	Type       string
	Branch     string
	Commitizen bool // bump + GitHub release with commitizen-action

	// PHP (Laravel, Symfony)
	PHPVersion  string
	Pint        bool     // Laravel
	PHPDirs     []string // dirs syntax-checked with php -l
	ViteAssets  bool     // also build front assets with Node (Vite/Encore) and ship them
	AssetMapper bool     // Symfony: compile AssetMapper assets (asset-map:compile)

	// Node (React, Angular, or a PHP app with ViteAssets)
	PackageManager  string // pnpm | npm
	PnpmVersion     string
	NodeVersion     string
	Lint            bool
	ViteVars        []string // repository variables required at build time
	PrivateRegistry bool     // .npmrc points at GitHub Packages (npm.pkg.github.com)

	// Static
	MkDocs        bool
	Requirements  bool // requirements.txt present (mkdocs)
	PythonVersion string
	BuildCommand  string // non-mkdocs static: optional build command
	OutputDir     string // directory that becomes the release

	// deploy-tools action version to pin (from this binary's build)
	ActionRef     string
	ActionVersion string
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// Detect inspects dir and returns sensible defaults.
func Detect(dir string) Options {
	abs, _ := filepath.Abs(dir)
	o := Options{
		App:           defaultApp(filepath.Base(abs)),
		Branch:        "main",
		Commitizen:    exists(dir, "cz.toml") || exists(dir, ".cz.toml") || pyprojectHasCommitizen(dir),
		NodeVersion:   "24",
		PnpmVersion:   "10",
		PythonVersion: "3.12",
		OutputDir:     "dist",
	}
	// Re-running init on a project that already deploys keeps its server
	// app name (repo "company-docs" may deploy as "docs").
	if app := existingAppName(dir); app != "" {
		o.App = app
	}
	o.PackageManager = "npm"
	if exists(dir, "pnpm-lock.yaml") {
		o.PackageManager = "pnpm"
	}
	pkg := readPackageJSON(dir)
	o.Lint = pkg.Scripts["lint"] != ""
	o.PrivateRegistry = npmrcUsesGitHubPackages(dir)
	o.ViteVars = viteVars(dir)

	switch {
	case exists(dir, "artisan") && exists(dir, "composer.json"):
		o.Type = Laravel
		comp := readComposer(dir)
		o.PHPVersion = phpVersion(comp.Require["php"])
		_, o.Pint = comp.RequireDev["laravel/pint"]
		for _, d := range []string{"app", "bootstrap", "config", "database", "routes", "tests"} {
			if exists(dir, d) {
				o.PHPDirs = append(o.PHPDirs, d)
			}
		}
	case exists(dir, "bin/console") && exists(dir, "composer.json") && hasKey(readComposer(dir).Require, "symfony/framework-bundle"):
		o.Type = Symfony
		comp := readComposer(dir)
		o.PHPVersion = phpVersion(comp.Require["php"])
		o.AssetMapper = hasKey(comp.Require, "symfony/asset-mapper")
		o.ViteAssets = pkg.Scripts["build"] != ""
		for _, d := range []string{"src", "config", "migrations", "public", "tests"} {
			if exists(dir, d) {
				o.PHPDirs = append(o.PHPDirs, d)
			}
		}
	case exists(dir, "mkdocs.yml"):
		o.Type = Static
		o.MkDocs = true
		o.Requirements = exists(dir, "requirements.txt")
		o.OutputDir = "site"
	case exists(dir, "angular.json"):
		o.Type = Angular
		o.OutputDir = angularOutputDir(dir)
	case pkg.Scripts["build"] != "" && (hasPrefixFile(dir, "vite.config.") || pkg.Deps["react"] != ""):
		o.Type = React
	default:
		o.Type = Static
		o.OutputDir = "."
	}
	return o
}

// App names follow /var/www/<app>: [a-zA-Z0-9_-].
func defaultApp(repo string) string {
	return regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(repo, "-")
}

type packageJSON struct {
	Scripts map[string]string `json:"scripts"`
	Deps    map[string]string `json:"dependencies"`
}

func readPackageJSON(dir string) packageJSON {
	var p packageJSON
	if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	if p.Scripts == nil {
		p.Scripts = map[string]string{}
	}
	if p.Deps == nil {
		p.Deps = map[string]string{}
	}
	return p
}

type composerJSON struct {
	Require    map[string]string `json:"require"`
	RequireDev map[string]string `json:"require-dev"`
}

func readComposer(dir string) composerJSON {
	var c composerJSON
	if b, err := os.ReadFile(filepath.Join(dir, "composer.json")); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

// phpVersion turns a constraint like "^8.4.1" or ">=8.2" into "8.4"/"8.2".
func phpVersion(constraint string) string {
	if m := regexp.MustCompile(`(\d+)\.(\d+)`).FindStringSubmatch(constraint); m != nil {
		return m[1] + "." + m[2]
	}
	return "8.4"
}

func hasKey(m map[string]string, k string) bool { _, ok := m[k]; return ok }

// angularOutputDir finds where `ng build` writes the browser files: the
// first (or default) project's outputPath, plus "browser" for the
// application builder (Angular 17+), which nests them there.
func angularOutputDir(dir string) string {
	var ng struct {
		DefaultProject string `json:"defaultProject"`
		Projects       map[string]struct {
			ProjectType string `json:"projectType"`
			Architect   map[string]struct {
				Builder string `json:"builder"`
				Options struct {
					OutputPath json.RawMessage `json:"outputPath"`
				} `json:"options"`
			} `json:"architect"`
		} `json:"projects"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "angular.json"))
	if err != nil || json.Unmarshal(b, &ng) != nil || len(ng.Projects) == 0 {
		return "dist"
	}
	name := ng.DefaultProject
	if _, ok := ng.Projects[name]; !ok {
		names := make([]string, 0, len(ng.Projects))
		for n, p := range ng.Projects {
			if p.ProjectType == "" || p.ProjectType == "application" {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			return "dist"
		}
		sort.Strings(names)
		name = names[0]
	}
	build := ng.Projects[name].Architect["build"]
	modern := strings.HasSuffix(build.Builder, ":application")
	base, browser := "dist/"+name, "browser"
	var s string
	var obj struct {
		Base    string  `json:"base"`
		Browser *string `json:"browser"`
	}
	switch {
	case json.Unmarshal(build.Options.OutputPath, &s) == nil && s != "":
		base = s
	case json.Unmarshal(build.Options.OutputPath, &obj) == nil && obj.Base != "":
		base = obj.Base
		if obj.Browser != nil {
			browser = *obj.Browser
		}
	}
	base = strings.TrimSuffix(strings.TrimPrefix(base, "./"), "/")
	if !modern || browser == "" {
		return base
	}
	return base + "/" + browser
}

func hasPrefixFile(dir, prefix string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, prefix+"*"))
	return len(m) > 0
}

func npmrcUsesGitHubPackages(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, ".npmrc"))
	return err == nil && strings.Contains(string(b), "npm.pkg.github.com")
}

func pyprojectHasCommitizen(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	return err == nil && strings.Contains(string(b), "[tool.commitizen]")
}

// viteVars lists the VITE_* keys declared in .env.example: they are needed
// at build time and become GitHub repository variables.
func viteVars(dir string) []string {
	f, err := os.Open(filepath.Join(dir, ".env.example"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	re := regexp.MustCompile(`^\s*(VITE_[A-Z0-9_]+)\s*=`)
	for sc.Scan() {
		if m := re.FindStringSubmatch(sc.Text()); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

var appNameRe = regexp.MustCompile(`^\s*APP_NAME:\s*["']?([a-zA-Z0-9_-]+)["']?\s*$`)

func existingAppName(dir string) string {
	for _, f := range WorkflowFiles(dir) {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if m := appNameRe.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
	return ""
}
