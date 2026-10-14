# deploy-tools

Deploy por releases com symlink para apps servidos pelo Apache, com build no
GitHub Actions e envio por SSH a um usuário `deploy` dedicado, compartilhado
entre aplicações. Cada deploy vira uma pasta em `releases/`, e `current`
aponta para a release ativa. As 3 releases mais recentes ficam em disco, e
as mais antigas são apagadas automaticamente.

```
/var/www/<app>/
├── current -> releases/20260911153000-a99d021
├── releases/
│   ├── 20260909101500-71d9ae2
│   ├── 20260910120000-db4e609
│   └── 20260911153000-a99d021
├── shared/          (.env, storage/ — sobrevivem entre deploys)
└── .deploy.conf     (tipo do app e hooks; opcional)
```

São dois binários Go:

- **`deploy-tools`**, no servidor, com dois papéis:
  - **Forced command da CI.** Chamado como `/usr/local/bin/deploy-ci` (um
    symlink para o binário), só aceita `deploy`, `rollback` e `list`, lidos
    do `SSH_ORIGINAL_COMMAND`.
  - **CLI de operação.** Rode `sudo deploy-tools` sem argumentos para abrir a
    interface de tela cheia, ou use os comandos abaixo. As ações ficam num
    menu à esquerda (Apps e Servidor). O painel à direita explica a ação
    selecionada, com o contexto atual e o comando equivalente. `↑↓` navega,
    `enter` abre, `esc` cancela e `q` sai. Sem `sudo`, a interface serve só
    para consulta. Com `ACCESSIBLE=1`, as mesmas ações aparecem num menu de
    linhas numeradas, próprio para leitores de tela.
- **`deploy-dev`**, na máquina dos desenvolvedores: gera e valida o
  `.github/workflows/deploy.yml` de cada projeto (veja
  [deploy-dev](#deploy-dev-configurar-o-deploy-de-um-projeto)).

Requisitos do servidor: Debian ou Ubuntu com Apache 2.4 (`a2ensite`,
`apache2ctl`, `sites-available/`), `mod_ssl` e, para Laravel, PHP via
mod_php.

## Setup de um servidor

Gere o par de chaves da CI **na sua máquina**, não no servidor, e baixe o
binário da [última release](https://github.com/vinicgobbi/deploy-tools/releases):

```bash
ssh-keygen -t ed25519 -C "ci-deploy" -f ci_deploy_key -N ""
curl -fLO https://github.com/vinicgobbi/deploy-tools/releases/download/vX.Y.Z/deploy-tools-linux-amd64
scp deploy-tools-linux-amd64 ci_deploy_key.pub usuario@servidor:/tmp/
ssh usuario@servidor 'chmod +x /tmp/deploy-tools-linux-amd64 && sudo /tmp/deploy-tools-linux-amd64 server setup --key /tmp/ci_deploy_key.pub'
```

O `server setup` é idempotente e faz o seguinte:

- Cria (ou corrige) o usuário `deploy`, no grupo `www-data` e com shell
  `/bin/sh`. Um shell de verdade é obrigatório: o sshd executa o `command=`
  forçado pelo shell de login, e o `nologin` quebra isso silenciosamente.
- Cria o `~/.ssh`.
- Instala o binário em `/usr/local/bin/deploy-tools` e o symlink
  `/usr/local/bin/deploy-ci`.
- Autoriza a chave pública com `command="/usr/local/bin/deploy-ci"` e
  `no-pty`.

Depois, guarde a chave **privada** como secret no GitHub (veja
[Secrets](#secrets-no-github)) e, se quiser, configure
[`/etc/deploy-tools.conf`](#configuração-do-servidor-etcdeploy-toolsconf).

Para atualizar um servidor já configurado, só troque o binário:

```bash
sudo /tmp/deploy-tools-linux-amd64 server install
```

O `server install` não mexe no usuário, nas chaves nem nos apps. As duas
trocas (binário e symlink) são `rename()` atômicos, então um deploy rodando
naquele momento não quebra. Se `/usr/local/bin/deploy-ci` já existia como
arquivo comum (um wrapper feito à mão), ele é guardado em `deploy-ci.bak`.

## Configuração do servidor (`/etc/deploy-tools.conf`)

Opcional. É um arquivo `CHAVE=valor`, do root, lido só pela CLI de operação
(o forced command da CI não precisa dele e nunca o lê). Ele é interpretado,
nunca executado como script.

```
# Domínio padrão: "app create api" sem --domain vira api.example.com
DOMAIN_SUFFIX=example.com

# Pastas extras onde procurar certificados (antes das padrão)
SSL_SEARCH_DIRS=/srv/certs:/opt/tls

# Ou um certificado fixo para todos os sites (desliga a busca automática)
#SSL_CERT_FILE=/etc/ssl/certs/wildcard.example.com.crt
#SSL_CERT_KEY=/etc/ssl/private/wildcard.example.com.key
#SSL_CERT_CHAIN=/etc/ssl/certs/wildcard.example.com.ca-bundle
```

Sem `DOMAIN_SUFFIX`, o `app create` exige `--domain`.

## Certificados TLS

Ao criar um site (ou mudar os domínios dele), o `deploy-tools` procura um
certificado que sirva. Ele só aceita um certificado que:

- cubra **todos** os domínios do site (wildcards valem);
- esteja dentro da validade;
- tenha a chave privada correspondente no disco. O casamento é feito pela
  chave pública, não pelo nome do arquivo.

Lugares onde procura, nesta ordem:

| Onde | O que |
|---|---|
| `SSL_SEARCH_DIRS` | pastas extras do `/etc/deploy-tools.conf` |
| `/etc/letsencrypt/live/*/` | `fullchain.pem` + `privkey.pem` do certbot |
| `/etc/ssl/certs`, `/etc/ssl/private`, `/etc/ssl` | layout Debian/Ubuntu |
| `/etc/pki/tls/certs`, `/etc/pki/tls/private` | layout RHEL |
| `/etc/apache2/ssl`, `/etc/httpd/ssl` | pastas comuns do Apache |

Entre os candidatos, um certificado emitido por CA ganha de um autoassinado,
um Let's Encrypt ganha dos outros, e depois vence o que expira por último.
Se o certificado não tiver a cadeia embutida, o `deploy-tools` procura um
CA bundle que o assine (de preferência na mesma pasta) e usa como
`SSLCertificateChainFile`.

A ordem de prioridade é: `--cert/--key` informados no comando, depois
`SSL_CERT_FILE` do `/etc/deploy-tools.conf`, depois a busca automática. Se
nada servir, o site é criado com o certificado autoassinado do sistema
(`ssl-cert-snakeoil`, do pacote `ssl-cert`), só para o Apache subir. O
`app status` avisa quando um site está nesse estado.

Depois de emitir ou renovar um certificado:

```bash
sudo certbot certonly --webroot -w /var/www/minha-api/current/public -d api.example.com
sudo deploy-tools app cert minha-api      # procura de novo e troca no site
sudo deploy-tools server certs            # lista o que foi encontrado, com domínios e validade
```

O `app cert` nunca troca um certificado real pelo autoassinado. Se nada
cobrir os domínios, o certificado atual é mantido e o comando avisa.

## Domínios

Um site pode ter vários domínios: o primeiro é o principal (`ServerName`), e
os outros viram `ServerAlias`. A porta 80 redireciona cada domínio para ele
mesmo em HTTPS. O `www.` não é mais adicionado automaticamente: inclua-o
como um domínio, se quiser.

```bash
sudo deploy-tools app create loja --type react -d loja.example.com -d www.loja.example.com
sudo deploy-tools app domain add loja shop.example.com
sudo deploy-tools app domain remove loja www.loja.example.com
deploy-tools app domain list loja
```

Ao adicionar ou remover domínios, se o certificado atual não cobrir o novo
conjunto, o `deploy-tools` procura um que cubra e troca. Se não achar, ele
mantém o atual e avisa quais domínios ficaram descobertos. Um site ativo é
alterado com a mesma sequência segura do `enable`: configtest antes e
depois, reload só se passar e o vhost anterior restaurado em caso de falha.
O resto do arquivo (edições feitas à mão) é preservado.

## Segurança do usuário compartilhado

Como a mesma chave SSH faz o deploy de várias aplicações, o acesso dela fica
restrito por `command=` no `authorized_keys`: a chave só consegue executar o
`deploy-ci`, nunca abrir um shell.

O próprio binário valida o nome do app (regex, e o diretório precisa já
existir) e o id da release (sem `/`, `.` ou `..`). Ele ignora variáveis de
ambiente: os caminhos são fixos no código.

Com a chave vazada, o pior cenário é publicar uma release em
`/var/www/<app_existente>`. Para Laravel com `.deploy.conf`, esse cenário
inclui rodar o `artisan` do código enviado como usuário `deploy`. O código PHP
publicado já seria executado pelo Apache de qualquer forma, mas vale ter isso
em mente. O `.deploy.conf` pertence ao root, então a CI não consegue ligar as
migrations sozinha.

Para auditar as chaves: `sudo deploy-tools key list`. Esse comando destaca
qualquer chave sem `command=`.

## Comandos

```
deploy-tools                                    # interface de tela cheia
deploy-tools app create <app> --type laravel|react|static [-d dominio]... [--cert c --key k [--chain b]] [--migrate-on-deploy] [--enable]
deploy-tools app migrate <app> --type ... [--vhost arquivo] [--yes]
deploy-tools app config <app> [--type t] [--migrate-on-deploy | --no-migrate-on-deploy]
deploy-tools app domain list|add|remove <app> [dominio]...
deploy-tools app cert <app> [--cert c --key k [--chain b]]
deploy-tools app enable <app>
deploy-tools app disable <app>
deploy-tools app list
deploy-tools app status <app>
deploy-tools release list <app>
deploy-tools release rollback <app> [release]   # sem release: volta para a anterior
deploy-tools key add <arquivo.pub>
deploy-tools key list
deploy-tools server install
deploy-tools server setup [--key arquivo.pub]
deploy-tools server certs
deploy-tools completion bash|zsh|fish
```

Todo comando tem `--help` com exemplos. Num terminal, faltou um argumento?
O comando pergunta, por exemplo, abrindo um select de tipo para
`app create minha-api`. Fora de um terminal (em scripts), ele falha com uma
mensagem de erro e um exemplo de uso.

As ações que mexem no que está no ar (`migrate`, `enable`, `disable` e
`rollback`) mostram um resumo e pedem confirmação. Use `--yes` para pular a
confirmação.

## Criar um app novo (`app create`)

```bash
sudo deploy-tools app create minha-api --type laravel -d api.example.com
sudo deploy-tools app create meu-front --type react -d app.example.com,www.app.example.com --enable
sudo deploy-tools app create docs --type static        # com DOMAIN_SUFFIX: docs.<sufixo>
```

O comando cria:

- `/var/www/<app>/{releases,shared}` com dono `deploy:www-data` e modo 2775.
  O setgid faz os arquivos herdarem o grupo `www-data`, então o Apache
  consegue ler.
- O `.deploy.conf`.
- O vhost em `sites-available/`: porta 443 com SSL (certificado escolhido
  como em [Certificados TLS](#certificados-tls)), mais uma porta 80 que
  redireciona para HTTPS.

Sem `--enable`, o vhost não é ativado. Confira DNS e certificado e depois rode
`sudo deploy-tools app enable <app>`. O `mod_rewrite` é ativado
automaticamente quando falta.

Para tirar um site do ar sem mexer nos arquivos, nas releases nem no deploy,
use `sudo deploy-tools app disable <app>`. Para voltar, use o `enable`.
Os dois usam o nome real do site em `sites-available`, mesmo que seja
diferente do app (por exemplo, `sistema-ssl.conf`), e seguem a mesma
sequência:

1. `apache2ctl configtest` **antes** de mexer. Se a config atual já tiver
   erro, nada é alterado, e o erro não é atribuído ao site.
2. `a2ensite` ou `a2dissite`.
3. `configtest` de novo e, só se passar, `systemctl reload apache2`. Se o
   teste ou o reload falhar, a mudança é desfeita com o comando oposto.

O `app create --enable` e o `app migrate` também recusam começar se a config
atual do Apache já estiver com erro.

| | Laravel | React | Static |
|---|---|---|---|
| `DocumentRoot` | `current/public` (PHP via mod_php, sem FPM) | `current` | `current` |
| Build esperado no tarball | checkout com `vendor/` já instalado | conteúdo de `dist/` (Vite) | qualquer site já buildado (ex.: mkdocs `site/`) |
| Rotas | `.htaccess` do próprio Laravel | `RewriteRule` de SPA no vhost (react-router funciona em refresh direto) | nenhuma regra especial |
| `shared/` | `.env` (vazio, você preenche) + `storage/` | não usa | não usa |
| Hooks no deploy | `storage:link`, `config:cache`, `route:cache`, `view:cache` (+ `migrate --force` opcional) | nenhum | nenhum |

## Migrar um app que já está em produção (`app migrate`)

Este caso é para apps que servem direto de `/var/www/<app>/`, um diretório
comum sem `releases/`:

```bash
sudo deploy-tools app migrate docs --type static
```

Antes de mexer em qualquer coisa, o comando mostra o plano: o vhost
encontrado, o DocumentRoot antigo → novo e o nome da release. Depois pede
confirmação. A migração **não derruba o site**:

1. Cria `/var/www/<app>/current -> .` dentro do diretório antigo, para que o
   caminho novo já sirva o conteúdo de hoje.
2. Faz backup do vhost (`<vhost>.bak-<release>`) e troca o `DocumentRoot` e o
   `<Directory>` para `current` ou `current/public`, garantindo
   `FollowSymLinks`. Em seguida roda `configtest` e `reload`. Se o
   `configtest` falhar, tudo é desfeito.
3. Ajusta os donos (`chown -R deploy:www-data`) com o site ainda no ar.
4. Reorganiza tudo só com `rename()` no mesmo filesystem, coisa de
   milissegundos:
   - o conteúdo vira `releases/<timestamp>-pre-migracao/`;
   - para Laravel, `.env` e `storage/` vão para `shared/` e voltam como
     symlink.
5. Grava o `.deploy.conf`.

O vhost é detectado em `sites-enabled/` pelo `DocumentRoot`. Se houver mais de
um, ou nenhum, indique o arquivo com `--vhost`. A partir daí, o próximo push
já cria a release seguinte normalmente.

## `.deploy.conf` e hooks de Laravel

`/var/www/<app>/.deploy.conf` é um arquivo `KEY=value` gravado pelo
`app create`, `app migrate` e `app config`:

```
TYPE=laravel
LARAVEL_MIGRATE=no
```

Com `TYPE=laravel`, todo deploy roda dentro da release nova, **antes** de
trocar o `current`:

```
php artisan storage:link
php artisan config:cache
php artisan route:cache
php artisan view:cache
php artisan migrate --force      # só com LARAVEL_MIGRATE=yes
```

Se qualquer hook falhar, a release nova é apagada e o `current` continua na
anterior, ou seja, o site não muda. O mesmo vale para um tarball corrompido.
Os comandos são fixos no binário: nada do tarball é executado como script.

Para apps sem `.deploy.conf` (por exemplo, migrados), ligue os hooks com:

```bash
sudo deploy-tools app config sistema --type laravel     # sem --type: detecta pelo current/
sudo deploy-tools app config sistema --migrate-on-deploy
```

## Por que existe `shared/`

Cada deploy é uma pasta nova em `releases/`. Para Laravel, isso apagaria
`.env` e `storage/` (uploads, sessions, cache, logs) a cada deploy. Para
evitar isso, tudo o que está em `/var/www/<app>/shared/` é symlinkado para
dentro da release recém-extraída, dotfiles incluídos, antes de trocar o
`current`.

Edite e faça upload só dentro de `shared/`, nunca dentro de uma
`releases/<id>/`. Elas são descartáveis e somem na poda.

## Secrets no GitHub

Configure em Settings → Secrets and variables → Actions, no repositório ou na
organização (para compartilhar entre vários apps no mesmo servidor):

| Secret | Valor |
|---|---|
| `DEPLOY_HOST` | IP ou hostname do servidor |
| `DEPLOY_USER` | `deploy` |
| `DEPLOY_SSH_KEY` | Conteúdo da chave **privada** (`ci_deploy_key`) |
| `DEPLOY_HOST_KEY` | Saída de `ssh-keyscan -H <host>` (fixa o host key e evita MITM sem precisar de `StrictHostKeyChecking=no`) |

O versionamento com Commitizen e a criação da release no GitHub usam o
`GITHUB_TOKEN` do próprio workflow: não há token extra para configurar. Se a
branch de deploy for protegida, libere o push do `github-actions[bot]` ou
troque por um PAT.

## `deploy-dev`: configurar o deploy de um projeto

O `deploy-dev` é o binário para a máquina dos desenvolvedores. Ele **só**
gera e valida o workflow de deploy do projeto. Os comandos de servidor (`app`,
`release`, `key`, `server`) e o forced command da CI não vêm nele.

### Instalação

Baixe o binário do seu sistema na
[última release](https://github.com/vinicgobbi/deploy-tools/releases) e rode
`install`. Ele se copia para uma pasta de programas do usuário, sem precisar
de admin, e garante que essa pasta está no PATH:

| Sistema | Arquivo da release | Instala em |
|---|---|---|
| Linux | `deploy-dev-linux-amd64` | `~/.local/bin/deploy-dev` |
| macOS Apple Silicon / Intel | `deploy-dev-darwin-arm64` / `-amd64` | `~/.local/bin/deploy-dev` |
| Windows | `deploy-dev-windows-amd64.exe` | `%LOCALAPPDATA%\Programs\deploy-dev\deploy-dev.exe` |

```bash
curl -fLo deploy-dev https://github.com/vinicgobbi/deploy-tools/releases/download/vX.Y.Z/deploy-dev-linux-amd64
chmod +x deploy-dev && ./deploy-dev install
```

No Windows, rode no PowerShell: `.\deploy-dev-windows-amd64.exe install`.

Como o `install` trata o PATH:

- **Linux/macOS:** se `~/.local/bin` não estiver no PATH, ele pergunta antes
  de adicionar a linha no `~/.zshrc`, `~/.bashrc` (no macOS,
  `~/.bash_profile`) ou `config.fish`, conforme o seu shell. A linha não é
  duplicada.
- **Windows:** ele adiciona a pasta ao PATH do usuário pelo registro, sem o
  `setx`, que corta PATHs longos.
- **macOS:** ele também remove a quarentena que o navegador aplica em
  downloads.

Opções: `--add-to-path` mexe no PATH sem perguntar, `--no-path` não mexe no
PATH e `--dir <pasta>` instala em outra pasta.

Para atualizar, baixe a versão nova e rode `install` de novo. Depois, confira
com `deploy-dev version`, que mostra também o SHA da action de deploy que o
`init` vai fixar.

### Uso

Dentro do repositório do projeto:

```bash
deploy-dev               # interface de tela cheia: gerar o workflow, checar, instalar no PATH
deploy-dev init          # mostra o que detectou, deixa ajustar e grava .github/workflows/deploy.yml
deploy-dev check         # valida os workflows (sai com código 1 se houver erro)
```

O `init` detecta:

- **Tipo:** `artisan` indica Laravel, Vite/React indica React e `mkdocs.yml`
  indica estático com mkdocs. Qualquer outro caso vira uma pasta pronta.
- **Commitizen:** presença de `cz.toml`, `.cz.toml` ou `[tool.commitizen]`
  no `pyproject.toml`.
- **Node:** pnpm ou npm, lint, os `VITE_*` do `.env.example` e o registry npm
  do GitHub Packages (`npm.pkg.github.com` no `.npmrc`).
- **PHP:** versão (do `composer.json`) e Pint.

Num projeto que já faz deploy, o `APP_NAME` existente é mantido. O workflow
gerado tem:

- actions fixadas por SHA, com a versão comentada;
- permissões mínimas do `GITHUB_TOKEN` (`contents: read`, ou `write` com
  Commitizen; `packages: read` com o registry do GitHub Packages);
- bump e release no GitHub com o `GITHUB_TOKEN`, quando o projeto usa
  Commitizen;
- a action de deploy deste repositório (`uses: vinicgobbi/deploy-tools@<sha>`),
  fixada no SHA da versão do próprio `deploy-dev`.

Outras opções: `--yes` aceita a detecção sem perguntar, `--stdout` só mostra o
resultado e `--force` sobrescreve.

O `check` aponta:

- actions sem SHA, ou com SHA mas sem a versão comentada;
- `RELEASE_ID` sem timestamp na frente (o servidor poda pelo nome);
- passo de SSH copiado, que pode virar a action;
- restos de Gitea: contexto `gitea.*`, URL em `uses:` e checkout local da
  action de deploy;
- bump sem a proteção contra loop;
- action de deploy numa versão mais antiga que a do `deploy-dev`.

Use `--strict` para que os avisos também falhem, por exemplo num workflow de
validação de PR.

## Action de deploy para os projetos

O passo de deploy por SSH é uma action composta deste repositório
([`action.yml`](action.yml)). Ela gera o release id (`<timestamp UTC>-<sufixo>`,
no formato que o `deploy-ci` usa para ordenar e podar), monta a chave e o
`known_hosts` num diretório temporário e envia o tarball:

```yaml
      - name: Deploy
        uses: vinicgobbi/deploy-tools@<sha-da-tag> # vX.Y.Z
        with:
          app: docs
          archive: site.tar.gz
          release-suffix: v${{ steps.cz.outputs.version }}   # opcional; padrão: SHA curto
          host: ${{ secrets.DEPLOY_HOST }}
          user: ${{ secrets.DEPLOY_USER }}
          ssh-key: ${{ secrets.DEPLOY_SSH_KEY }}
          host-key: ${{ secrets.DEPLOY_HOST_KEY }}
```

Fixe sempre pelo SHA do commit da tag, com a versão comentada ao lado. A
action evolui junto com o `deploy-ci`: se o protocolo da CI mudar, as duas
coisas mudam na mesma versão.

## Rollback

Não precisa de um novo build: o rollback só reaponta o symlink para uma
release que ainda esteja em disco (só as 3 últimas ficam guardadas). No
servidor:

```bash
deploy-tools release list docs
sudo deploy-tools release rollback docs                         # volta para a anterior
sudo deploy-tools release rollback docs 20260910120000-db4e609
```

Pela chave da CI:

```bash
ssh -i ci_deploy_key deploy@servidor "list docs"
ssh -i ci_deploy_key deploy@servidor "rollback docs 20260910120000-db4e609"
```

## Desenvolvimento

O projeto precisa de Go 1.26+.

```bash
make test       # go test ./...
make vet
make build      # bin/deploy-tools (servidor; versão = git describe)
make build-dev  # bin/deploy-dev para esta máquina
make dist       # todos os binários da release em dist/, com .sha256
```

### Versionamento e release

O projeto usa [Commitizen](https://commitizen-tools.github.io/commitizen/)
com Conventional Commits. A versão fica só nas tags git (`vX.Y.Z`), e o
`CHANGELOG.md` é gerado a partir dos commits. **A release é automática**: a
cada push em `main`, o workflow
[`.github/workflows/release.yml`](.github/workflows/release.yml) faz o
seguinte:

1. Roda `go vet` e `go test`.
2. Roda `cz bump`. Se houver `feat:`, `fix:` ou BREAKING desde a última tag,
   commita `bump: version X → Y`, atualiza o `CHANGELOG.md`, cria a tag e dá
   push com o `GITHUB_TOKEN`. Commits só de `docs:`, `ci:`, `chore:` ou
   `refactor:` não geram release.
3. Builda os binários da tag e publica a release no GitHub, com a seção do
   CHANGELOG como notas e os arquivos anexados (cada um com seu `.sha256`).
   Os binários vão do runner direto para a release, e no fim o job exclui
   qualquer artefato do run, então nada fica ocupando armazenamento de
   Actions.
4. Autocorreção: se não houver nada para versionar, mas a última tag ainda
   não tiver release (por exemplo, porque um run anterior falhou depois do
   bump), essa tag é publicada no próximo push.

Então basta usar commits no padrão (`cz commit` ajuda) e dar push.

`fix:` gera um patch, `feat:` gera um minor, e `feat!:` ou um rodapé
`BREAKING CHANGE:` gera um major. Mudanças no protocolo da CI (comandos,
argumentos, mensagens do `deploy-ci`) são sempre breaking, porque quebram
os workflows existentes.

```
cmd/deploy-tools/     main do servidor: decide entre o modo CI e a CLI
cmd/deploy-dev/       main do desktop: só internal/devcli (sem código de servidor)
internal/devcli/      comandos do deploy-dev (init, check, install, modo guiado)
internal/project/     detecção do projeto, template do deploy.yml e regras do check
internal/ci/          forced command (SSH_ORIGINAL_COMMAND)
internal/release/     deploy, rollback, list, prune, shared/, lock
internal/appconf/     .deploy.conf
internal/apache/      template do vhost, edição de DocumentRoot/domínios/certificado, configtest/reload
internal/tlscert/     busca de certificados TLS no servidor
internal/provision/   create, migrate, config, domínios, certificado, install, setup, chaves
internal/cli/         comandos cobra + itens da interface de tela cheia
internal/tui/         interface de tela cheia (bubbletea) dos dois binários
internal/ui/          prompts e saída formatada
internal/config/      caminhos, convenções fixas e /etc/deploy-tools.conf
internal/validate/    validação de nome de app, release, tipo e domínio
```
