package project

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Level int

const (
	Info Level = iota
	Warn
	Error
)

func (l Level) String() string { return [...]string{"info", "aviso", "erro"}[l] }

type Finding struct {
	Level Level
	File  string
	Line  int
	Msg   string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.File, f.Line, f.Level, f.Msg)
}

var (
	shaRe       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	usesRe      = regexp.MustCompile(`^\s*-?\s*uses:\s*["']?([^"'\s#]+)["']?\s*(#.*)?$`)
	giteaCtxRe  = regexp.MustCompile(`\bgitea\.(event|ref|sha|server_url|repository|actor)\b`)
	releaseIDRe = regexp.MustCompile(`RELEASE_ID=["']?([^"'\n]*)`)
)

// WorkflowFiles lists the GitHub Actions workflow files under dir.
func WorkflowFiles(dir string) []string {
	var out []string
	for _, ext := range []string{"*.yml", "*.yaml"} {
		m, _ := filepath.Glob(filepath.Join(dir, ".github", "workflows", ext))
		out = append(out, m...)
	}
	sort.Strings(out)
	return out
}

// Check validates every workflow in dir. actionRef/actionVersion describe
// the deploy action this binary was built from (for "outdated" hints).
func Check(dir, actionRef, actionVersion string) ([]Finding, error) {
	files := WorkflowFiles(dir)
	if len(files) == 0 {
		return []Finding{{Level: Error, File: dir, Msg: "nenhum workflow em .github/workflows — gere um com `deploy-dev init`"}}, nil
	}
	var all []Finding
	for _, f := range files {
		fs, err := checkFile(dir, f, actionRef, actionVersion)
		if err != nil {
			return nil, err
		}
		all = append(all, fs...)
	}
	return all, nil
}

func checkFile(dir, path, actionRef, actionVersion string) ([]Finding, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rel, _ := filepath.Rel(dir, path)
	content := string(b)
	var out []Finding
	add := func(l Level, line int, format string, a ...any) {
		out = append(out, Finding{Level: l, File: rel, Line: line, Msg: fmt.Sprintf(format, a...)})
	}

	sc := bufio.NewScanner(strings.NewReader(content))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if m := usesRe.FindStringSubmatch(line); m != nil {
			checkUses(add, n, m[1], m[2])
			if repo, sha, ok := strings.Cut(m[1], "@"); ok && repo == DeployToolsRepo && shaRe.MatchString(sha) &&
				shaRe.MatchString(actionRef) && sha != actionRef {
				add(Info, n, "action de deploy em %s; este deploy-dev usa %s (%s) — atualize se quiser as mudanças",
					strings.TrimSpace(strings.TrimPrefix(m[2], "#")), actionVersion, actionRef[:7])
			}
		}
		if giteaCtxRe.MatchString(line) {
			add(Error, n, "contexto gitea.* não existe no GitHub Actions: use github.*")
		}
		if m := releaseIDRe.FindStringSubmatch(line); m != nil && !strings.Contains(m[1], "date -u +%Y%m%d%H%M%S") {
			add(Error, n, "RELEASE_ID precisa começar com $(date -u +%%Y%%m%%d%%H%%M%%S): o servidor ordena e poda as releases pelo nome")
		}
		if strings.Contains(line, `"deploy $APP_NAME`) || strings.Contains(line, `"deploy ${APP_NAME`) {
			add(Warn, n, "passo de deploy por SSH copiado — troque pela action do deploy-tools (deploy-dev init gera o trecho)")
		}
	}

	usesCommitizen := strings.Contains(content, "commitizen") || strings.Contains(content, "cz bump")
	if usesCommitizen && !strings.Contains(content, "'bump:')") {
		add(Warn, 1, "workflow faz bump com commitizen mas não ignora os commits \"bump:\" — se o push usar um PAT, dispara um loop")
	}
	if strings.Contains(content, "./.deploy-tools") {
		add(Warn, 1, "o deploy-tools é público: troque o checkout + `uses: ./.deploy-tools` por `uses: %s@<sha>` (deploy-dev init gera o trecho)", DeployToolsRepo)
	}
	return out, nil
}

func checkUses(add func(Level, int, string, ...any), n int, ref, comment string) {
	if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
		return
	}
	if strings.HasPrefix(ref, "https://") {
		add(Error, n, "%s: o GitHub Actions não aceita URL em `uses:` — use dono/repo@sha", ref)
		return
	}
	at := strings.LastIndex(ref, "@")
	if at < 0 {
		add(Error, n, "%s sem versão: fixe em um SHA completo", ref)
		return
	}
	version := ref[at+1:]
	if !shaRe.MatchString(version) {
		add(Error, n, "%s usa referência mutável %q: fixe no SHA completo do commit, com a versão comentada", ref[:at], version)
		return
	}
	if strings.TrimSpace(strings.TrimPrefix(comment, "#")) == "" {
		add(Warn, n, "%s fixado por SHA sem comentário de versão (ex.: \"# v4.4.0\")", ref[:at])
	}
}
