package project

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var update = flag.Bool("update", false, "rewrite golden files")

const testRef = "66ac5741e772bfd2c2afb58b1d36527da520f3f6"

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixtures mirror typical projects of each kind.
func fixture(t *testing.T, kind string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), map[string]string{
		"laravel": "api", "react": "front", "mkdocs": "company-docs", "plain": "site",
		"symfony": "loja", "angular": "painel",
	}[kind])
	switch kind {
	case "laravel":
		write(t, dir, "artisan", "#!/usr/bin/env php")
		write(t, dir, "composer.json", `{"require":{"php":"^8.4.1"},"require-dev":{"laravel/pint":"^1"}}`)
		write(t, dir, "cz.toml", "[tool.commitizen]\n")
		for _, d := range []string{"app", "config", "routes", "tests"} {
			write(t, dir, d+"/x.php", "<?php")
		}
	case "react":
		write(t, dir, "package.json", `{"scripts":{"build":"tsc -b && vite build","lint":"eslint ."},"dependencies":{"react":"^19"}}`)
		write(t, dir, "pnpm-lock.yaml", "")
		write(t, dir, "vite.config.ts", "")
		write(t, dir, "cz.toml", "")
		write(t, dir, ".npmrc", "@acme:registry=https://npm.pkg.github.com\n")
		write(t, dir, ".env.example", "# c\nVITE_API_URL_API=x\nVITE_API_URL_GQL=y\nOTHER=1\n")
	case "symfony":
		write(t, dir, "bin/console", "#!/usr/bin/env php")
		write(t, dir, "composer.json", `{"require":{"php":">=8.3","symfony/framework-bundle":"7.3.*","symfony/asset-mapper":"7.3.*"}}`)
		write(t, dir, "public/index.php", "<?php")
		for _, d := range []string{"src", "config", "migrations"} {
			write(t, dir, d+"/x.php", "<?php")
		}
	case "angular":
		write(t, dir, "package.json", `{"scripts":{"build":"ng build","lint":"ng lint"},"dependencies":{"@angular/core":"^20"}}`)
		write(t, dir, "package-lock.json", "{}")
		write(t, dir, "angular.json", `{"projects":{"painel":{"projectType":"application","architect":{"build":{"builder":"@angular/build:application","options":{"outputPath":"dist/painel"}}}}}}`)
	case "mkdocs":
		write(t, dir, "mkdocs.yml", "site_name: x")
		write(t, dir, "requirements.txt", "mkdocs")
	case "plain":
		write(t, dir, "index.html", "hi")
	}
	return dir
}

func TestDetect(t *testing.T) {
	l := Detect(fixture(t, "laravel"))
	if l.Type != Laravel || l.App != "api" || l.PHPVersion != "8.4" || !l.Pint || !l.Commitizen ||
		strings.Join(l.PHPDirs, ",") != "app,config,routes,tests" {
		t.Errorf("laravel: %+v", l)
	}
	if l.ViteAssets {
		t.Error("laravel sem package.json não tem assets para buildar")
	}
	lv := fixture(t, "laravel")
	write(t, lv, "package.json", `{"scripts":{"build":"vite build"}}`)
	write(t, lv, "pnpm-lock.yaml", "")
	if l := Detect(lv); !l.ViteAssets || l.PackageManager != "pnpm" {
		t.Errorf("laravel com vite: %+v", l)
	}
	r := Detect(fixture(t, "react"))
	if r.Type != React || r.App != "front" || r.PackageManager != "pnpm" || !r.Lint || !r.PrivateRegistry ||
		strings.Join(r.ViteVars, ",") != "VITE_API_URL_API,VITE_API_URL_GQL" || r.OutputDir != "dist" {
		t.Errorf("react: %+v", r)
	}
	s := Detect(fixture(t, "symfony"))
	if s.Type != Symfony || s.PHPVersion != "8.3" || !s.AssetMapper || s.ViteAssets || s.Pint ||
		strings.Join(s.PHPDirs, ",") != "src,config,migrations,public" {
		t.Errorf("symfony: %+v", s)
	}
	a := Detect(fixture(t, "angular"))
	if a.Type != Angular || a.OutputDir != "dist/painel/browser" || !a.Lint || a.PackageManager != "npm" {
		t.Errorf("angular: %+v", a)
	}
	m := Detect(fixture(t, "mkdocs"))
	if m.Type != Static || !m.MkDocs || !m.Requirements || m.OutputDir != "site" || m.Commitizen {
		t.Errorf("mkdocs: %+v", m)
	}
	p := Detect(fixture(t, "plain"))
	if p.Type != Static || p.MkDocs || p.OutputDir != "." {
		t.Errorf("plain: %+v", p)
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch (rode com -update):\n%s", name, got)
	}
}

func TestRenderGoldenAndClean(t *testing.T) {
	for _, kind := range []string{"laravel", "symfony", "react", "angular", "mkdocs", "plain"} {
		t.Run(kind, func(t *testing.T) {
			dir := fixture(t, kind)
			o := Detect(dir)
			o.ActionRef, o.ActionVersion = testRef, "v1.2.0"
			got, err := Render(o)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, "deploy-"+kind+".yml", got)

			var doc map[string]any
			if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
				t.Fatalf("YAML inválido: %v\n%s", err, got)
			}
			// What init generates must pass check without warnings/errors
			// (info about the action version is fine).
			write(t, dir, ".github/workflows/deploy.yml", got)
			fs, err := Check(dir, testRef, "v1.2.0")
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range fs {
				if f.Level > Info {
					t.Errorf("finding no gerado: %s", f)
				}
			}
		})
	}
}

// The generated laravel package step must be valid shell (excludes quoted).
func TestLaravelPackageStepIsValidShell(t *testing.T) {
	o := Detect(fixture(t, "laravel"))
	o.ActionRef, o.ActionVersion = testRef, "v1.2.0"
	got, _ := Render(o)
	var doc struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatal(err)
	}
	for _, s := range doc.Jobs["build"].Steps {
		if s.Run == "" {
			continue
		}
		if out, err := exec.Command("bash", "-n", "-c", s.Run).CombinedOutput(); err != nil {
			t.Errorf("step %q: bash -n: %v %s", s.Name, err, out)
		}
	}
}

func TestCheckFindsProblemsInLegacyWorkflow(t *testing.T) {
	dir := t.TempDir()
	// A hand-written workflow ported from Gitea.
	write(t, dir, ".github/workflows/deploy.yml", `name: Build and deploy docs
on:
  push:
    branches: [main]
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@a26af69be951a213d495a4c3e4e4022e16d87065
      - uses: https://gitea.com/actions/gitea-release-action@5a83c90484ef4355121df5eb60558c8ce66e81ac # v1.3.7
      - name: Deploy over SSH
        run: |
          RELEASE_ID="${GITHUB_SHA::7}"
          ssh "$DEPLOY_USER@$DEPLOY_HOST" \
              "deploy $APP_NAME $RELEASE_ID" < site.tar.gz
      - uses: commitizen-tools/commitizen-action@338bbd841b75aaee6bf5340e1fa12f6ab58ff9ff # 0.27.1
`)
	fs, err := Check(dir, testRef, "v1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		got = append(got, f.String())
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		`:9: erro: actions/checkout usa referência mutável "v4"`,
		`:10: aviso: actions/setup-python fixado por SHA sem comentário`,
		`:11: erro: https://gitea.com/actions/gitea-release-action@5a83c90484ef4355121df5eb60558c8ce66e81ac: o GitHub Actions não aceita URL`,
		`:14: erro: RELEASE_ID precisa começar`,
		`:16: aviso: passo de deploy por SSH copiado`,
		`:1: aviso: workflow faz bump com commitizen mas não ignora`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("faltou %q em:\n%s", want, all)
		}
	}
	if len(fs) != 6 {
		t.Errorf("%d findings, want 6:\n%s", len(fs), all)
	}
}

func TestCheckOutdatedAction(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".github/workflows/deploy.yml", `jobs:
  x:
    steps:
      - uses: vinicgobbi/deploy-tools@1111111111111111111111111111111111111111 # v1.0.0
        if: ${{ github.ref == 'refs/heads/main' }}
`)
	fs, _ := Check(dir, testRef, "v1.2.0")
	if len(fs) != 1 || fs[0].Level != Info || !strings.Contains(fs[0].Msg, "v1.0.0") || !strings.Contains(fs[0].Msg, "v1.2.0") {
		t.Fatalf("findings = %v", fs)
	}
}

func TestDetectKeepsExistingAppName(t *testing.T) {
	dir := fixture(t, "mkdocs")
	write(t, dir, ".github/workflows/deploy.yml", "env:\n  APP_NAME: docs\n")
	if got := Detect(dir).App; got != "docs" {
		t.Fatalf("App = %q, want docs", got)
	}
}

func TestCheckFlagsGiteaLeftovers(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".github/workflows/deploy.yml", `jobs:
  x:
    if: "!startsWith(gitea.event.head_commit.message, 'bump:')"
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          repository: vinicgobbi/deploy-tools
          path: .deploy-tools
      - uses: ./.deploy-tools
`)
	fs, _ := Check(dir, testRef, "v1.2.0")
	var got []string
	for _, f := range fs {
		got = append(got, f.String())
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{":3: erro: contexto gitea.*", ":1: aviso: o deploy-tools é público"} {
		if !strings.Contains(all, want) {
			t.Errorf("faltou %q em:\n%s", want, all)
		}
	}
}

func TestAngularOutputDir(t *testing.T) {
	for name, c := range map[string]struct{ json, want string }{
		"legacy browser builder":    {`{"projects":{"app":{"architect":{"build":{"builder":"@angular-devkit/build-angular:browser","options":{"outputPath":"dist/app"}}}}}}`, "dist/app"},
		"application, default path": {`{"projects":{"app":{"architect":{"build":{"builder":"@angular-devkit/build-angular:application","options":{}}}}}}`, "dist/app/browser"},
		"object outputPath":         {`{"projects":{"app":{"architect":{"build":{"builder":"@angular/build:application","options":{"outputPath":{"base":"out","browser":""}}}}}}}`, "out"},
		"defaultProject wins":       {`{"defaultProject":"b","projects":{"a":{},"b":{"architect":{"build":{"builder":"x:application","options":{"outputPath":"./www/"}}}}}}`, "www/browser"},
		"libraries skipped":         {`{"projects":{"a-lib":{"projectType":"library"},"web":{"projectType":"application"}}}`, "dist/web"},
	} {
		dir := t.TempDir()
		write(t, dir, "angular.json", c.json)
		if got := angularOutputDir(dir); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestRemoteHostAndMatchForge(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:vinicgobbi/site.git":           "github.com",
		"https://Git.Empresa.com/acme/site.git":        "git.empresa.com",
		"ssh://git@git.empresa.com:2222/acme/site.git": "git.empresa.com",
		"https://git.empresa.com:3000/acme/site":       "git.empresa.com:3000",
		"gitea@git.empresa.com:acme/site.git":          "git.empresa.com",
		"/srv/repos/site.git":                          "",
		"":                                             "",
	} {
		if got := RemoteHost(remote); got != want {
			t.Errorf("RemoteHost(%q) = %q, want %q", remote, got, want)
		}
	}
	forges := []Forge{GitHubCom,
		{Name: "empresa", Kind: Gitea, URL: "https://git.empresa.com"},
		{Name: "lab", Kind: Forgejo, URL: "https://lab.acme.dev:3000"}}
	for remote, want := range map[string]string{
		"git@github.com:a/b.git":                 "github",
		"ssh://git@git.empresa.com:2222/a/b.git": "empresa",
		"https://lab.acme.dev:3000/a/b":          "lab",
		"ssh://git@lab.acme.dev:22/a/b.git":      "lab", // ssh port is not the web one
		"https://gitlab.com/a/b":                 "",
	} {
		f, ok := MatchForge(forges, remote)
		if f.Name != want || ok != (want != "") {
			t.Errorf("MatchForge(%q) = %q %v, want %q", remote, f.Name, ok, want)
		}
	}
}

func TestForgeUses(t *testing.T) {
	cases := []struct {
		f    Forge
		want string
	}{
		{Forge{}, Checkout.Uses()},
		{Forge{Kind: Gitea, URL: "https://g.x"}, Checkout.Uses()}, // instance default (github.com)
		{Forge{Kind: Gitea, URL: "https://g.x", ActionsURL: "https://g.x/mirror/"}, "https://g.x/mirror/" + Checkout.Uses()},
		{Forge{Kind: Forgejo, URL: "https://f.x"}, "https://github.com/" + Checkout.Uses()},
	}
	for _, c := range cases {
		if got := c.f.Uses(Checkout); got != c.want {
			t.Errorf("%+v: %q, want %q", c.f, got, c.want)
		}
	}
}

func TestForgeValidate(t *testing.T) {
	ok := Forge{Name: "empresa", Kind: Gitea, URL: "https://git.empresa.com"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Forge{
		{Name: "Empresa X", Kind: Gitea, URL: "https://g.x"},
		{Name: "e", Kind: "gitlab", URL: "https://g.x"},
		{Name: "e", Kind: Gitea, URL: "git.empresa.com"},
		{Name: "e", Kind: Gitea, URL: "https://g.x", ActionsURL: "mirror"},
		{Name: "e", Kind: Gitea, URL: "https://g.x", Runner: "a b"},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v deveria ser inválida", bad)
		}
	}
}

var (
	giteaForge   = Forge{Name: "empresa", Kind: Gitea, URL: "https://git.empresa.com"}
	forgejoForge = Forge{Name: "lab", Kind: Forgejo, URL: "https://code.lab.dev", Runner: "docker"}
)

func TestRenderGiteaGoldenAndClean(t *testing.T) {
	for _, c := range []struct {
		kind  string
		forge Forge
	}{
		{"laravel", giteaForge}, {"react", giteaForge}, {"plain", giteaForge},
		{"symfony", forgejoForge}, {"mkdocs", forgejoForge},
	} {
		t.Run(c.forge.Kind+"-"+c.kind, func(t *testing.T) {
			dir := fixture(t, c.kind)
			o := Detect(dir)
			o.ActionRef, o.ActionVersion = testRef, "v1.2.0"
			o.Forge = c.forge
			got, err := Render(o)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, c.forge.Kind+"-"+c.kind+".yml", got)

			var doc struct {
				Jobs map[string]struct {
					RunsOn string `yaml:"runs-on"`
					Steps  []struct {
						Name string `yaml:"name"`
						Uses string `yaml:"uses"`
						Run  string `yaml:"run"`
					} `yaml:"steps"`
				} `yaml:"jobs"`
			}
			if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
				t.Fatalf("YAML inválido: %v\n%s", err, got)
			}
			if _, ok := doc.Jobs["build"]; ok {
				t.Error("no Gitea/Forgejo, build e deploy são um job só")
			}
			for name, job := range doc.Jobs {
				if job.RunsOn != c.forge.RunsOn() {
					t.Errorf("%s: runs-on %q", name, job.RunsOn)
				}
				for _, s := range job.Steps {
					if strings.Contains(s.Uses, "artifact") || strings.Contains(s.Run, "gh ") {
						t.Errorf("%s/%s: passo só do GitHub: %s%s", name, s.Name, s.Uses, s.Run)
					}
					if s.Run != "" {
						if out, err := exec.Command("bash", "-n", "-c", s.Run).CombinedOutput(); err != nil {
							t.Errorf("%s/%s: bash -n: %v %s", name, s.Name, err, out)
						}
					}
				}
			}
			write(t, dir, filepath.Join(c.forge.WorkflowDir(), "deploy.yml"), got)
			fs, err := Check(dir, testRef, "v1.2.0")
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range fs {
				if f.Level > Info {
					t.Errorf("finding no gerado: %s", f)
				}
			}
		})
	}
}

func TestCheckGiteaRules(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitea/workflows/deploy.yml", `jobs:
  x:
    if: "!startsWith(gitea.event.head_commit.message, 'bump:')"
    steps:
      - uses: https://gitea.com/actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
      - uses: https://gitea.com/actions/setup-node@v4
      - uses: https://github.com/vinicgobbi/deploy-tools@1111111111111111111111111111111111111111 # v1.0.0
`)
	fs, _ := Check(dir, testRef, "v1.2.0")
	var got []string
	for _, f := range fs {
		got = append(got, f.String())
	}
	all := strings.Join(got, "\n")
	if !strings.Contains(all, `:6: erro: https://gitea.com/actions/setup-node usa referência mutável "v4"`) ||
		!strings.Contains(all, ":7: info: action de deploy em v1.0.0") || len(fs) != 2 {
		t.Errorf("findings:\n%s", all)
	}
}
