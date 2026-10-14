package project

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// The template uses [[ ]] delimiters so the workflow's own ${{ }}
// expressions can be written literally.
var workflowTmpl = template.Must(template.New("deploy").Delims("[[", "]]").Funcs(template.FuncMap{
	"join": strings.Join,
}).Parse(`# Gerado por deploy-dev [[.ActionVersion]] (deploy-dev init). Pode editar à
# vontade; rode "deploy-dev check" para validar contra as convenções.
name: Release and deploy

on:
  push:
    branches:
      - [[.Branch]]

env:
  APP_NAME: [[.App]]

jobs:
[[- if .Commitizen]]
  # Um job só: valida, versiona (Commitizen) e publica a partir do mesmo
  # checkout. O commit "bump:" e a tag são enviados com o GITHUB_TOKEN, que
  # não dispara outro workflow; o "if" protege caso troque por um PAT.
[[- end]]
  release-and-deploy:
    runs-on: ubuntu-latest
[[- if .Commitizen]]
    if: "!startsWith(github.event.head_commit.message, 'bump:')"
[[- end]]

    permissions:
      contents: [[if .Commitizen]]write[[else]]read[[end]]
[[- if .PrivateRegistry]]
      packages: read
[[- end]]

    steps:
      - uses: [[.Pin.Checkout]]
[[- if .Commitizen]]
        with:
          fetch-depth: 0
[[- end]]
[[- if eq .Type "laravel"]]

      - uses: [[.Pin.SetupPHP]]
        with:
          php-version: "[[.PHPVersion]]"
          coverage: none

      # O servidor tem as extensões de produção; o runner só precisa
      # validar e montar o vendor/, sem executar a aplicação.
      - name: Install dependencies
        run: composer install --no-interaction --no-progress --prefer-dist --ignore-platform-req='ext-*'
[[- if .PHPDirs]]

      - name: Check PHP syntax
        run: find [[join .PHPDirs " "]] -name '*.php' -print0 | xargs -0 -n50 -P4 php -l > /dev/null
[[- end]]
[[- if .Pint]]

      - name: Check code style (Pint)
        run: vendor/bin/pint --test
[[- end]]
[[- end]]
[[- if .NeedsNode]]
[[- if eq .PackageManager "pnpm"]]

      - uses: [[.Pin.PnpmSetup]]
        with:
          version: [[.PnpmVersion]]
[[- end]]

      - uses: [[.Pin.SetupNode]]
        with:
          node-version: "[[.NodeVersion]]"
[[- if .PrivateRegistry]]

      # GitHub Packages (npm.pkg.github.com). O token vai no ~/.npmrc do
      # runner, fora do repositório: o "cz bump" faz "git commit -a" e
      # levaria o token junto num .npmrc versionado.
      - name: Configure registry auth
        env:
          NODE_AUTH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: printf '\n//npm.pkg.github.com/:_authToken=%s\n' "$NODE_AUTH_TOKEN" >> "$HOME/.npmrc"
[[- end]]

      - name: Install Node dependencies
        run: [[.NodeInstall]]
[[- if and .Lint (eq .Type "react")]]

      - name: Lint
        run: [[.NodeRun]] lint
[[- end]]
[[- end]]
[[- if eq .Type "static"]]
[[- if .MkDocs]]

      - uses: [[.Pin.SetupPython]]
        with:
          python-version: "[[.PythonVersion]]"

      - name: Install dependencies
        run: [[if .Requirements]]pip install -r requirements.txt[[else]]pip install mkdocs[[end]]
[[- end]]
[[- end]]
[[- if .Commitizen]]

      - name: Bump version with Commitizen
        id: cz
        uses: [[.Pin.Commitizen]]
        with:
          github_token: ${{ secrets.GITHUB_TOKEN }}
          changelog_increment_filename: body.md
          push: true
          git_name: github-actions[bot]
          git_email: 41898282+github-actions[bot]@users.noreply.github.com

      - name: Create GitHub release
        if: steps.cz.outputs.previous_version != steps.cz.outputs.next_version
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          TAG: v${{ steps.cz.outputs.version }}
        run: gh release create "$TAG" --title "$TAG" --notes-file body.md --verify-tag
[[- end]]
[[- if .BuildsVite]]

      - name: Build
[[- if .ViteVars]]
        env:
[[- range .ViteVars]]
          [[.]]: ${{ vars.[[.]] }}
[[- end]]
[[- end]]
        run: |
          set -euo pipefail
[[- if .ViteVars]]
          for var in [[join .ViteVars " "]]; do
            if [ -z "${!var}" ]; then
              echo "Variável $var não configurada no repositório." >&2
              exit 1
            fi
          done
[[- end]]
          [[.NodeRun]] build
[[- end]]
[[- if eq .Type "static"]]
[[- if .MkDocs]]

      - name: Build site
        run: mkdocs build --strict --site-dir [[.OutputDir]]
[[- else if .BuildCommand]]

      - name: Build
        run: [[.BuildCommand]]
[[- end]]
[[- end]]
[[- if eq .Type "laravel"]]

      # Reaproveita o vendor/ já baixado: remove os pacotes de dev e
      # regenera o autoload otimizado.
      - name: Prune dev dependencies
        run: composer install --no-interaction --no-progress --prefer-dist --no-dev --optimize-autoloader --ignore-platform-req='ext-*'
[[- end]]

      - name: Package release
[[- if eq .Type "laravel"]]
        run: |
          tar -czf "$RUNNER_TEMP/release.tar.gz" \
[[- range .LaravelExcludes]]
            --exclude='[[.]]' \
[[- end]]
            .
[[- else]]
        run: tar -czf "$RUNNER_TEMP/release.tar.gz" -C [[.OutputDir]] [[if eq .OutputDir "."]]--exclude=./.git --exclude=./.github [[end]].
[[- end]]

      # Gera o release id (timestamp + sufixo), monta a chave num diretório
      # temporário e envia o tarball ao deploy-ci do servidor.
      - name: Deploy
        uses: [[.DeployToolsRepo]]@[[.ActionRef]] # [[.ActionVersion]]
        with:
          app: ${{ env.APP_NAME }}
          archive: ${{ runner.temp }}/release.tar.gz
[[- if .Commitizen]]
          release-suffix: v${{ steps.cz.outputs.version }}
[[- end]]
          host: ${{ secrets.DEPLOY_HOST }}
          user: ${{ secrets.DEPLOY_USER }}
          ssh-key: ${{ secrets.DEPLOY_SSH_KEY }}
          host-key: ${{ secrets.DEPLOY_HOST_KEY }}
`))

type tmplData struct {
	Options
	Pin             map[string]string
	DeployToolsRepo string
}

func (d tmplData) NeedsNode() bool  { return d.Type == React || (d.Type == Laravel && d.ViteAssets) }
func (d tmplData) BuildsVite() bool { return d.NeedsNode() }

func (d tmplData) NodeInstall() string {
	if d.PackageManager == "pnpm" {
		return "pnpm install --frozen-lockfile --prefer-offline"
	}
	return "npm ci"
}

func (d tmplData) NodeRun() string {
	if d.PackageManager == "pnpm" {
		return "pnpm"
	}
	return "npm run"
}

// LaravelExcludes keeps dev-only files out of the release. Node files go
// too unless the app ships Vite assets (public/build is kept either way).
func (d tmplData) LaravelExcludes() []string {
	ex := []string{
		"./.git", "./.github", "./tests", "./node_modules",
		"./.env", "./.env.*", "./phpunit.xml", "./cz.toml", "./.cz.toml", "./body.md",
		"./*.code-workspace",
	}
	if !d.ViteAssets {
		ex = append(ex, "./package.json", "./package-lock.json", "./pnpm-lock.yaml",
			"./pnpm-workspace.yaml", "./vite.config.js", "./vite.config.ts")
	}
	return ex
}

var appRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Validate checks the options before rendering.
func (o Options) Validate() error {
	switch {
	case !appRe.MatchString(o.App):
		return fmt.Errorf("nome do app inválido: %q (use só letras, números, - e _)", o.App)
	case o.Type != Laravel && o.Type != React && o.Type != Static:
		return fmt.Errorf("tipo inválido: %q", o.Type)
	case o.Branch == "":
		return errors.New("branch não informado")
	case !shaRe.MatchString(o.ActionRef):
		return fmt.Errorf("ref da action de deploy inválido: %q (precisa ser um SHA completo)", o.ActionRef)
	case o.Type != Laravel && o.OutputDir == "":
		return errors.New("diretório de saída do build não informado")
	case o.PackageManager != "pnpm" && o.PackageManager != "npm":
		return fmt.Errorf("gerenciador de pacotes inválido: %q", o.PackageManager)
	}
	for _, v := range o.ViteVars {
		if !regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`).MatchString(v) {
			return fmt.Errorf("variável inválida: %q", v)
		}
	}
	return nil
}

// Render produces the deploy.yml content.
func Render(o Options) (string, error) {
	if err := o.Validate(); err != nil {
		return "", err
	}
	d := tmplData{
		Options:         o,
		DeployToolsRepo: DeployToolsRepo,
		Pin: map[string]string{
			"Checkout": Checkout.Uses(), "SetupPython": SetupPython.Uses(), "SetupNode": SetupNode.Uses(),
			"PnpmSetup": PnpmSetup.Uses(), "SetupPHP": SetupPHP.Uses(),
			"Commitizen": Commitizen.Uses(),
		},
	}
	var buf bytes.Buffer
	if err := workflowTmpl.Execute(&buf, d); err != nil {
		return "", err
	}
	return buf.String(), nil
}
