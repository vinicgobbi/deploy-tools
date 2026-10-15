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
#
# Etapas: [[.Stages]].
# Cada job roda numa máquina nova: o pacote vai do build para o deploy como
# artefato, apagado logo depois do deploy.
name: Release and deploy

on:
  push:
    branches:
      - [[.Branch]]

# Um deploy por vez nesta branch: um push novo espera o anterior terminar.
concurrency:
  group: deploy-${{ github.ref }}
  cancel-in-progress: false

env:
  APP_NAME: [[.App]]

jobs:
[[- if .Validates]]
  validate:
    runs-on: ubuntu-latest
[[- if .Commitizen]]
    if: "!startsWith(github.event.head_commit.message, 'bump:')"
[[- end]]
    permissions:
      contents: read
[[- if .PrivateRegistry]]
      packages: read
[[- end]]

    steps:
      - uses: [[.Pin.Checkout]]
[[- if .IsPHP]]
[[- template "php-setup" .]]

      # O servidor tem as extensões de produção; o runner só precisa
      # validar, sem executar a aplicação.
      - name: Install dependencies
        run: composer install --no-interaction --no-progress --prefer-dist --ignore-platform-req='ext-*'[[if eq .Type "symfony"]] --no-scripts[[end]]
[[- if .PHPDirs]]

      - name: Check PHP syntax
        run: find [[join .PHPDirs " "]] -name '*.php' -print0 | xargs -0 -n50 -P4 php -l > /dev/null
[[- end]]
[[- if .Pint]]

      - name: Check code style (Pint)
        run: vendor/bin/pint --test
[[- end]]
[[- end]]
[[- if .NodeLint]]
[[- template "node-setup" .]]

      - name: Lint
        run: [[.NodeRun]] lint
[[- end]]
[[- end]]
[[- if .Commitizen]]

  # Versiona (Commitizen) e cria a release no GitHub. O commit "bump:" e a
  # tag vão com o GITHUB_TOKEN, que não dispara outro workflow; o "if"
  # protege caso troque por um PAT.
  release:
[[- if .Validates]]
    needs: validate
[[- else]]
    if: "!startsWith(github.event.head_commit.message, 'bump:')"
[[- end]]
    runs-on: ubuntu-latest
    permissions:
      contents: write
    outputs:
      version: ${{ steps.cz.outputs.version }}
      bumped: ${{ steps.cz.outputs.previous_version != steps.cz.outputs.next_version }}

    steps:
      - uses: [[.Pin.Checkout]]
        with:
          fetch-depth: 0

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

  build:
[[- if .Commitizen]]
    needs: release
[[- else if .Validates]]
    needs: validate
[[- end]]
    runs-on: ubuntu-latest
    permissions:
      contents: read
[[- if .PrivateRegistry]]
      packages: read
[[- end]]
    outputs:
      artifact-id: ${{ steps.upload.outputs.artifact-id }}

    steps:
      - uses: [[.Pin.Checkout]]
[[- if .Commitizen]]
        with:
          # A tag criada pelo bump (com o CHANGELOG), ou o commit do push.
          ref: ${{ needs.release.outputs.bumped == 'true' && format('v{0}', needs.release.outputs.version) || github.sha }}
[[- end]]
[[- if .IsPHP]]
[[- template "php-setup" .]]

      # Só as dependências de produção, com o autoload otimizado.
      - name: Install dependencies
[[- if eq .Type "symfony"]]
        env:
          APP_ENV: prod
        run: composer install --no-interaction --no-progress --prefer-dist --no-dev --optimize-autoloader --no-scripts --ignore-platform-req='ext-*'

      # O cache (var/cache) é gerado no servidor, no caminho da release; aqui
      # só os arquivos públicos dos bundles[[if .AssetMapper]] e o AssetMapper[[end]].
      - name: Build public assets
        env:
          APP_ENV: prod
          APP_DEBUG: "0"
        run: |
          php bin/console assets:install public --no-interaction
[[- if .AssetMapper]]
          php bin/console asset-map:compile
[[- end]]
[[- else]]
        run: composer install --no-interaction --no-progress --prefer-dist --no-dev --optimize-autoloader --ignore-platform-req='ext-*'
[[- end]]
[[- end]]
[[- if .NeedsNode]]
[[- template "node-setup" .]]

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

      - uses: [[.Pin.SetupPython]]
        with:
          python-version: "[[.PythonVersion]]"

      - name: Install dependencies
        run: [[if .Requirements]]pip install -r requirements.txt[[else]]pip install mkdocs[[end]]

      - name: Build site
        run: mkdocs build --strict --site-dir [[.OutputDir]]
[[- else if .BuildCommand]]

      - name: Build
        run: [[.BuildCommand]]
[[- end]]
[[- end]]

      - name: Package release
[[- if .IsPHP]]
        run: |
          tar -czf "$RUNNER_TEMP/release.tar.gz" \
[[- range .PHPExcludes]]
            --exclude='[[.]]' \
[[- end]]
            .
[[- else]]
        run: tar -czf "$RUNNER_TEMP/release.tar.gz" -C [[.OutputDir]] [[if eq .OutputDir "."]]--exclude=./.git --exclude=./.github [[end]].
[[- end]]

      # Leva o pacote ao job de deploy, que o apaga depois de publicar (se o
      # deploy falhar, ele expira em 1 dia).
      - name: Upload package
        id: upload
        uses: [[.Pin.UploadArtifact]]
        with:
          name: release
          path: ${{ runner.temp }}/release.tar.gz
          retention-days: 1
          compression-level: 0
          if-no-files-found: error

  deploy:
    needs: [[if .Commitizen]][build, release][[else]]build[[end]]
    runs-on: ubuntu-latest
    permissions:
      actions: write # apagar o artefato depois do deploy

    steps:
      - name: Download package
        uses: [[.Pin.DownloadArtifact]]
        with:
          name: release
          path: ${{ runner.temp }}

      # Gera o release id (timestamp + sufixo), monta a chave num diretório
      # temporário e envia o tarball ao deploy-ci do servidor.
      - name: Deploy
        uses: [[.DeployToolsRepo]]@[[.ActionRef]] # [[.ActionVersion]]
        with:
          app: ${{ env.APP_NAME }}
          archive: ${{ runner.temp }}/release.tar.gz
[[- if .Commitizen]]
          release-suffix: v${{ needs.release.outputs.version }}
[[- end]]
          host: ${{ secrets.DEPLOY_HOST }}
          user: ${{ secrets.DEPLOY_USER }}
          ssh-key: ${{ secrets.DEPLOY_SSH_KEY }}
          host-key: ${{ secrets.DEPLOY_HOST_KEY }}

      - name: Delete package artifact
        env:
          GH_TOKEN: ${{ github.token }}
        run: gh api -X DELETE "repos/${{ github.repository }}/actions/artifacts/${{ needs.build.outputs.artifact-id }}"
[[- define "php-setup"]]

      - uses: [[.Pin.SetupPHP]]
        with:
          php-version: "[[.PHPVersion]]"
          coverage: none
[[- end]]
[[- define "node-setup"]]
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
      # runner, fora do repositório.
      - name: Configure registry auth
        env:
          NODE_AUTH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: printf '\n//npm.pkg.github.com/:_authToken=%s\n' "$NODE_AUTH_TOKEN" >> "$HOME/.npmrc"
[[- end]]

      - name: Install Node dependencies
        run: [[.NodeInstall]]
[[- end]]
`))

type tmplData struct {
	Options
	Pin             map[string]string
	DeployToolsRepo string
}

func (d tmplData) IsPHP() bool     { return d.Type == Laravel || d.Type == Symfony }
func (d tmplData) IsSPA() bool     { return d.Type == React || d.Type == Angular }
func (d tmplData) NeedsNode() bool { return d.IsSPA() || (d.IsPHP() && d.ViteAssets) }
func (d tmplData) NodeLint() bool  { return d.Lint && d.IsSPA() }

// Validates: there is something to check before versioning and building.
func (d tmplData) Validates() bool {
	return (d.IsPHP() && (len(d.PHPDirs) > 0 || d.Pint)) || d.NodeLint()
}

// Stages names the jobs in order, for the header comment.
func (d tmplData) Stages() string {
	var st []string
	if d.Validates() {
		st = append(st, "validate")
	}
	if d.Commitizen {
		st = append(st, "release")
	}
	return strings.Join(append(st, "build", "deploy"), " → ")
}

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

// PHPExcludes keeps dev-only files out of the release. Node files go too
// unless the app ships built front assets (public/build, public/assets).
func (d tmplData) PHPExcludes() []string {
	ex := []string{"./.git", "./.github", "./tests", "./node_modules"}
	switch d.Type {
	case Symfony:
		// .env (defaults) is part of the app; only the local overrides stay
		// out. var/ is rebuilt on the server.
		ex = append(ex, "./var", "./.env.local", "./.env.*.local", "./.env.test",
			"./phpunit.xml", "./phpunit.xml.dist", "./phpunit.dist.xml", "./.php-cs-fixer.cache")
	default:
		ex = append(ex, "./.env", "./.env.*", "./phpunit.xml")
	}
	ex = append(ex, "./cz.toml", "./.cz.toml", "./body.md", "./*.code-workspace")
	if !d.ViteAssets {
		ex = append(ex, "./package.json", "./package-lock.json", "./pnpm-lock.yaml",
			"./pnpm-workspace.yaml", "./vite.config.js", "./vite.config.ts")
		if d.Type == Symfony {
			ex = append(ex, "./webpack.config.js")
		}
	}
	return ex
}

var appRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Validate checks the options before rendering.
func (o Options) Validate() error {
	switch {
	case !appRe.MatchString(o.App):
		return fmt.Errorf("nome do app inválido: %q (use só letras, números, - e _)", o.App)
	case o.Type != Laravel && o.Type != Symfony && o.Type != React && o.Type != Angular && o.Type != Static:
		return fmt.Errorf("tipo inválido: %q", o.Type)
	case o.Branch == "":
		return errors.New("branch não informado")
	case !shaRe.MatchString(o.ActionRef):
		return fmt.Errorf("ref da action de deploy inválido: %q (precisa ser um SHA completo)", o.ActionRef)
	case o.Type != Laravel && o.Type != Symfony && o.OutputDir == "":
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
			"Commitizen": Commitizen.Uses(), "UploadArtifact": UploadArtifact.Uses(), "DownloadArtifact": DownloadArtifact.Uses(),
		},
	}
	var buf bytes.Buffer
	if err := workflowTmpl.Execute(&buf, d); err != nil {
		return "", err
	}
	return buf.String(), nil
}
