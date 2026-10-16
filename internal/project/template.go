package project

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// The template uses [[ ]] delimiters so the workflow's own ${{ }}
// expressions can be written literally. "github" and "gitea" are the two
// layouts; the jobs and steps they share are the named templates below.
var workflowTmpl = template.Must(template.New("deploy").Delims("[[", "]]").Funcs(template.FuncMap{
	"join": strings.Join,
}).Parse(`[[if .Forge.IsGitHub]][[template "github" .]][[else]][[template "gitea" .]][[end]]
[[- define "github"]]# Gerado por deploy-dev [[.ActionVersion]] (deploy-dev init). Pode editar à
# vontade; rode "deploy-dev check" para validar contra as convenções.
#
# Etapas: [[.Stages]].
# Cada job roda numa máquina nova: o pacote vai do build para o deploy como
# artefato, apagado logo depois do deploy.
name: Release and deploy
[[- template "on" .]]

jobs:
[[- template "validate-job" .]]
[[- template "release-job" .]]

  build:
[[- if .Commitizen]]
    needs: release
[[- else if .Validates]]
    needs: validate
[[- end]]
    runs-on: [[.Forge.RunsOn]]
    permissions:
      contents: read
[[- if .PrivateRegistry]]
      packages: read
[[- end]]
    outputs:
      artifact-id: ${{ steps.upload.outputs.artifact-id }}

    steps:
[[- template "build-steps" .]]

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
    runs-on: [[.Forge.RunsOn]]
    permissions:
      actions: write # apagar o artefato depois do deploy

    steps:
      - name: Download package
        uses: [[.Pin.DownloadArtifact]]
        with:
          name: release
          path: ${{ runner.temp }}
[[- template "deploy-step" .]]

      - name: Delete package artifact
        env:
          GH_TOKEN: ${{ github.token }}
        run: gh api -X DELETE "repos/${{ github.repository }}/actions/artifacts/${{ needs.build.outputs.artifact-id }}"
[[- end]]
[[- define "gitea"]]# Gerado por deploy-dev [[.ActionVersion]] (deploy-dev init) para o [[.ForgeName]]
# ([[.Forge.Host]]). Pode editar à vontade; rode "deploy-dev check" para
# validar contra as convenções.
#
# Etapas: [[.Stages]].
# Build e deploy rodam no mesmo job: as actions oficiais de artefato (v4+)
# não funcionam no [[.ForgeName]], e assim o pacote não sai do runner.
name: Release and deploy
[[- template "on" .]]

jobs:
[[- template "validate-job" .]]
[[- template "release-job" .]]

  deploy:
[[- if .Commitizen]]
    needs: release
[[- else if .Validates]]
    needs: validate
[[- end]]
    runs-on: [[.Forge.RunsOn]]
    permissions:
      contents: read

    steps:
[[- template "build-steps" .]]
[[- template "deploy-step" .]]
[[- end]]
[[- define "on"]]

on:
  push:
    branches:
      - [[.Branch]]

# Um deploy por vez nesta branch: um push novo espera o anterior terminar.
[[- if eq .Forge.Kind "gitea"]]
# O Gitea respeita isto a partir da 1.26; nas anteriores, o deploy-ci do
# servidor já espera um deploy em andamento terminar.
[[- else if eq .Forge.Kind "forgejo"]]
# Se o Forgejo ignorar isto, o deploy-ci do servidor já espera um deploy em
# andamento terminar.
[[- end]]
[[- if and .Commitizen (not .Forge.IsGitHub)]]
# O push do "bump:" dispara outro run (com um token de bot, ou conforme a
# instância). Ele fica num grupo à parte: no mesmo grupo, o [[.ForgeName]]
# cancelaria o job de deploy ainda pendente do run que fez o bump.
concurrency:
  group: ${{ startsWith(github.event.head_commit.message, 'bump:') && format('bump-{0}', github.sha) || format('deploy-{0}', github.ref) }}
  cancel-in-progress: false
[[- else]]
concurrency:
  group: deploy-${{ github.ref }}
  cancel-in-progress: false
[[- end]]

env:
  APP_NAME: [[.App]]
[[- end]]
[[- define "validate-job"]]
[[- if .Validates]]
  validate:
    runs-on: [[.Forge.RunsOn]]
[[- if .Commitizen]]
    if: "!startsWith(github.event.head_commit.message, 'bump:')"
[[- end]]
    permissions:
      contents: read
[[- if .PackagesPermission]]
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
[[- end]]
[[- define "release-job"]]
[[- if .Commitizen]]
[[- if .Forge.IsGitHub]]

  # Versiona (Commitizen) e cria a release no GitHub. O commit "bump:" e a
  # tag vão com o GITHUB_TOKEN, que não dispara outro workflow; o "if"
  # protege caso troque por um PAT.
[[- else]]

  # Versiona (Commitizen) e cria a release no [[.ForgeName]]. O commit "bump:"
  # e a tag vão com o token do Actions (ou o CI_BOT_TOKEN); o "if" ignora o
  # run que esse push dispara.
[[- end]]
  release:
[[- if .Validates]]
    needs: validate
[[- else]]
    if: "!startsWith(github.event.head_commit.message, 'bump:')"
[[- end]]
    runs-on: [[.Forge.RunsOn]]
    permissions:
      contents: write
[[- if eq .Forge.Kind "gitea"]]
      releases: write
[[- end]]
    outputs:
      version: ${{ steps.cz.outputs.version }}
[[- if .Forge.IsGitHub]]
      bumped: ${{ steps.cz.outputs.previous_version != steps.cz.outputs.next_version }}
[[- else]]
      bumped: ${{ steps.cz.outputs.bumped }}
[[- end]]

    steps:
[[- if .Forge.IsGitHub]]
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
          git_name: [[.BotName]]
          git_email: [[.BotEmail]]

      - name: Create GitHub release
        if: steps.cz.outputs.previous_version != steps.cz.outputs.next_version
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          TAG: v${{ steps.cz.outputs.version }}
        run: gh release create "$TAG" --title "$TAG" --notes-file body.md --verify-tag
[[- else]]
      - uses: [[.Pin.Checkout]]
        with:
          fetch-depth: 0 # o cz precisa do histórico e das tags
          # Faz o push do bump e da tag. Com a branch protegida, crie o
          # secret CI_BOT_TOKEN com o token de um usuário bot que possa
          # fazer push nela.
          token: ${{ secrets.CI_BOT_TOKEN || secrets.GITHUB_TOKEN }}

      # O cz roda direto no runner, sem a action do Commitizen: ela é do
      # tipo Docker, que nem todo runner do [[.ForgeName]] executa.
      - uses: [[.Pin.SetupPython]]
        with:
          python-version: "[[.PythonVersion]]"

      - name: Install Commitizen
        run: pip install commitizen==[[.CommitizenVersion]]

      - name: Bump version with Commitizen
        id: cz
        run: |
          git config user.name "[[.BotName]]"
          git config user.email "[[.BotEmail]]"
          set +e
          cz bump --yes --changelog --changelog-to-stdout > body.md
          code=$?
          set -e
          version="$(cz version --project)"
          case "$code" in
            0)
              git push --atomic origin "HEAD:[[.Branch]]" "v$version"
              echo "bumped=true" >> "$GITHUB_OUTPUT"
              ;;
            3|21) # 3 = nenhum commit novo, 21 = só commits que não geram versão
              echo "Nada para versionar."
              echo "bumped=false" >> "$GITHUB_OUTPUT"
              ;;
            *)
              exit "$code"
              ;;
          esac
          echo "version=$version" >> "$GITHUB_OUTPUT"

      # Pela API do [[.ForgeName]] (o gh só fala com o GitHub). Se o job for
      # re-executado e a release já existir, não cria outra.
      - name: Create [[.ForgeName]] release
        if: steps.cz.outputs.bumped == 'true'
        env:
          TOKEN: ${{ secrets.CI_BOT_TOKEN || secrets.GITHUB_TOKEN }}
          API: ${{ github.server_url }}/api/v1/repos/${{ github.repository }}/releases
          TAG: v${{ steps.cz.outputs.version }}
        run: |
          set -euo pipefail
          status=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: token $TOKEN" "$API/tags/$TAG")
          if [ "$status" = 200 ]; then
            echo "A release $TAG já existe."
            exit 0
          fi
          python3 -c 'import json, os; t = os.environ["TAG"]; print(json.dumps({"tag_name": t, "name": t, "body": open("body.md").read()}))' |
            curl -fsS -X POST -H "Authorization: token $TOKEN" -H "Content-Type: application/json" --data @- "$API" > /dev/null
          echo "Release $TAG criada."
[[- end]]
[[- end]]
[[- end]]
[[- define "build-steps"]]
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
        run: tar -czf "$RUNNER_TEMP/release.tar.gz" -C [[.OutputDir]] [[if eq .OutputDir "."]][[range .RootExcludes]]--exclude=[[.]] [[end]][[end]].
[[- end]]
[[- end]]
[[- define "deploy-step"]]

      # Gera o release id (timestamp + sufixo), monta a chave num diretório
      # temporário e envia o tarball ao deploy-ci do servidor.
      - name: Deploy
        uses: [[.DeployUses]]
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
[[- end]]
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
[[- if .Forge.IsGitHub]]

      # GitHub Packages (npm.pkg.github.com). O token vai no ~/.npmrc do
      # runner, fora do repositório.
      - name: Configure registry auth
        env:
          NODE_AUTH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
[[- else]]

      # GitHub Packages (npm.pkg.github.com): o token do [[.ForgeName]] não vale
      # lá, então use um token do GitHub com read:packages no secret
      # GH_PACKAGES_TOKEN. Ele vai no ~/.npmrc do runner, fora do repositório.
      - name: Configure registry auth
        env:
          NODE_AUTH_TOKEN: ${{ secrets.GH_PACKAGES_TOKEN }}
[[- end]]
        run: printf '\n//npm.pkg.github.com/:_authToken=%s\n' "$NODE_AUTH_TOKEN" >> "$HOME/.npmrc"
[[- end]]

      - name: Install Node dependencies
        run: [[.NodeInstall]]
[[- end]]
`))

type tmplData struct {
	Options
	Pin        map[string]string
	DeployUses string
}

func (d tmplData) IsPHP() bool     { return d.Type == Laravel || d.Type == Symfony }
func (d tmplData) IsSPA() bool     { return d.Type == React || d.Type == Angular }
func (d tmplData) NeedsNode() bool { return d.IsSPA() || (d.IsPHP() && d.ViteAssets) }
func (d tmplData) NodeLint() bool  { return d.Lint && d.IsSPA() }

// PackagesPermission: the GitHub token reads GitHub Packages (elsewhere a
// separate token does, and there is no such permission).
func (d tmplData) PackagesPermission() bool { return d.PrivateRegistry && d.Forge.IsGitHub() }

func (d tmplData) ForgeName() string { return KindName(d.Forge.Kind) }

func (d tmplData) CommitizenVersion() string { return CommitizenPy }

// BotName and BotEmail author the "bump:" commit.
func (d tmplData) BotName() string {
	if d.Forge.IsGitHub() {
		return "github-actions[bot]"
	}
	return d.Forge.Kind + "-actions"
}

func (d tmplData) BotEmail() string {
	if d.Forge.IsGitHub() {
		return "41898282+github-actions[bot]@users.noreply.github.com"
	}
	host, _, _ := strings.Cut(d.Forge.Host(), ":")
	return d.Forge.Kind + "-actions@noreply." + host
}

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
	if !d.Forge.IsGitHub() {
		return strings.Join(append(st, "deploy (build + publicação)"), " → ")
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

// RootExcludes are the repository's own dirs, kept out when the release is
// the repository root (OutputDir ".").
func (d tmplData) RootExcludes() []string {
	ex := []string{"./.git", "./.github"}
	if !d.Forge.IsGitHub() {
		ex = append(ex, "./"+strings.Split(filepath.ToSlash(d.Forge.WorkflowDir()), "/")[0])
	}
	return ex
}

// PHPExcludes keeps dev-only files out of the release. Node files go too
// unless the app ships built front assets (public/build, public/assets).
func (d tmplData) PHPExcludes() []string {
	ex := append(d.RootExcludes(), "./tests", "./node_modules")
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
	if o.Forge.Name != "" {
		if err := o.Forge.Validate(); err != nil {
			return err
		}
	}
	for _, v := range o.ViteVars {
		if !regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`).MatchString(v) {
			return fmt.Errorf("variável inválida: %q", v)
		}
	}
	return nil
}

// Render produces the deploy.yml content for o.Forge.
func Render(o Options) (string, error) {
	if err := o.Validate(); err != nil {
		return "", err
	}
	f := o.Forge
	d := tmplData{
		Options:    o,
		DeployUses: f.Uses(Pin{DeployToolsRepo, o.ActionRef, o.ActionVersion}),
		Pin: map[string]string{
			"Checkout": f.Uses(Checkout), "SetupPython": f.Uses(SetupPython), "SetupNode": f.Uses(SetupNode),
			"PnpmSetup": f.Uses(PnpmSetup), "SetupPHP": f.Uses(SetupPHP),
			"Commitizen": f.Uses(Commitizen), "UploadArtifact": f.Uses(UploadArtifact), "DownloadArtifact": f.Uses(DownloadArtifact),
		},
	}
	var buf bytes.Buffer
	if err := workflowTmpl.Execute(&buf, d); err != nil {
		return "", err
	}
	return buf.String(), nil
}
