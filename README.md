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
`apache2ctl`, `sites-available/`), `mod_ssl` e, para Laravel e Symfony, PHP
via mod_php.

## Setup de um servidor

Baixe o binário da [última release](https://github.com/vinicgobbi/deploy-tools/releases)
no servidor e rode o setup, gerando a chave da CI ali mesmo:

```bash
curl -fLO https://github.com/vinicgobbi/deploy-tools/releases/download/vX.Y.Z/deploy-tools-linux-amd64
chmod +x deploy-tools-linux-amd64
sudo ./deploy-tools-linux-amd64 server setup --generate-key
```

Primeiro, o setup pergunta **como o GitHub vai acessar o servidor**: o
hostname, um dos IPs das interfaces ou outro endereço digitado (o IP público,
quando o servidor está atrás de NAT, ou um DNS). Isso importa porque o valor
vira o secret `DEPLOY_HOST` e também a linha do `known_hosts` em
`DEPLOY_HOST_KEY`: o SSH só aceita o host key pelo mesmo nome ou IP usado na
conexão. A resposta fica em `DEPLOY_HOST` no `/etc/deploy-tools.conf`. Em
scripts, use `--host <endereço>`.

Com `--generate-key`, a chave pública é autorizada na hora e o par é gravado
**na pasta de onde você chamou o comando**: `ci_deploy_key` (modo 600) e
`ci_deploy_key.pub`, ou `ci_deploy_key-2`… se já existir (nunca
sobrescreve). Com `sudo`, os arquivos ficam com o seu usuário, então o `gh`
rodando como você consegue ler. Os comandos `gh secret set` mostrados no fim
já apontam para o caminho absoluto da chave. Ao lado dela, fica também o
`github-secrets.sh`, que cadastra os quatro secrets no repositório que você
passar:

```bash
sh github-secrets.sh vinicgobbi/website                 # usa a chave gravada ao lado
sh github-secrets.sh --org acme                         # secrets da organização
sh github-secrets.sh vinicgobbi/website ~/ci_deploy_key # chave em outro lugar
```

O script roda em qualquer máquina com o `gh` logado. Depois de cadastrar os
secrets, apague a chave privada e o script.

Para escolher outro lugar, use `--key-out arquivo`. Para gerar outra chave
depois, rode `sudo deploy-tools key generate`; com `--print-only`, nada é
gravado e a chave aparece uma única vez na tela.

Se preferir gerar o par na sua máquina, a chave privada nunca passa pelo
servidor:

```bash
ssh-keygen -t ed25519 -C "ci-deploy" -f ci_deploy_key -N ""
scp deploy-tools-linux-amd64 ci_deploy_key.pub usuario@servidor:/tmp/
ssh usuario@servidor 'chmod +x /tmp/deploy-tools-linux-amd64 && sudo /tmp/deploy-tools-linux-amd64 server setup --key /tmp/ci_deploy_key.pub'
```

Na TUI, o "Setup de servidor" e o "Adicionar chave" oferecem as duas opções.

O `server setup` é idempotente e faz o seguinte:

- Cria (ou corrige) o usuário `deploy`, no grupo `www-data` e com shell
  `/bin/sh`. Um shell de verdade é obrigatório: o sshd executa o `command=`
  forçado pelo shell de login, e o `nologin` quebra isso silenciosamente.
- Cria o `~/.ssh`.
- Instala o binário em `/usr/local/bin/deploy-tools` e o symlink
  `/usr/local/bin/deploy-ci`.
- Gera (`--generate-key`) ou recebe (`--key`) a chave da CI e autoriza a
  pública com `command="/usr/local/bin/deploy-ci"` e `no-pty`.

Ao final, o `server setup` com chave (e também o `key add` e o
`key generate`) mostra como devem
ficar os secrets do GitHub para este servidor (veja
[Secrets](#secrets-no-github)). Se quiser, configure também
[`/etc/deploy-tools.conf`](#configuração-do-servidor-etcdeploy-toolsconf).

O `deploy-tools` identifica se está rodando a cópia instalada ou um binário
solto (por exemplo, a release recém-baixada em `/tmp`). O `deploy-tools
version` e o `deploy-tools server status` mostram isso, junto com a versão
instalada e se o binário solto é mais novo ou mais antigo. A interface de
tela cheia indica "binário solto" no cabeçalho.

### Atualizações automáticas

A partir da v0.10.0, os dois binários se atualizam sozinhos. No máximo uma
vez por dia, ao rodar um comando, eles consultam a última release no GitHub
(com timeout curto, sem travar o comando). Se houver versão nova, baixam o
binário do seu sistema, **conferem o SHA-256** publicado na release e o
instalam. A versão nova vale a partir da próxima execução.

- **deploy-tools**: como root e com o binário instalado, instala sozinho
  (como o `server install`: troca atômica do binário e do `deploy-ci`). Sem
  root, só avisa. O forced command da CI nunca consulta nada.
- **deploy-dev**: atualiza a cópia instalada no PATH do usuário.
- Na mão: `sudo deploy-tools update` e `deploy-dev update` (`--check` só
  verifica).
- Para desligar: `AUTO_UPDATE=no` no `/etc/deploy-tools.conf`, ou as
  variáveis `DEPLOY_TOOLS_NO_UPDATE=1` e `DEPLOY_DEV_NO_UPDATE=1`. Builds de
  desenvolvimento (`dev`, `-dirty`) nunca se atualizam.

Chamado pelo nome (`deploy-tools`, `sudo deploy-tools`, `deploy-dev`), o
binário é o instalado: a TUI não mostra "Instalar/atualizar", e o modo
guiado do `deploy-dev` não mostra "Instalar no PATH". Chamado por um caminho
(`./deploy-tools-linux-amd64`), a opção continua lá.

Para atualizar na mão um servidor já configurado, também dá para trocar o binário:

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
# Endereço pelo qual o runner do GitHub alcança o servidor (secret DEPLOY_HOST)
DEPLOY_HOST=deploy.example.com

# Domínios coringa (gerenciados com "deploy-tools server domain"):
# "app create api" sem --domain vira api.<um deles>
BASE_DOMAINS=vinicgobbi.dev.br,example.com
# Certificado *.<domínio> de um coringa: <domínio> <certificado> <chave> [cadeia]
BASE_CERT=vinicgobbi.dev.br /etc/letsencrypt/live/vinicgobbi.dev.br/fullchain.pem /etc/letsencrypt/live/vinicgobbi.dev.br/privkey.pem

# Pastas extras onde procurar certificados (antes das padrão)
SSL_SEARCH_DIRS=/srv/certs:/opt/tls

# Ou um certificado fixo para todos os sites (desliga a busca automática)
#SSL_CERT_FILE=/etc/ssl/certs/wildcard.example.com.crt
#SSL_CERT_KEY=/etc/ssl/private/wildcard.example.com.key
#SSL_CERT_CHAIN=/etc/ssl/certs/wildcard.example.com.ca-bundle
```

Sem domínio coringa, o `app create` exige `--domain`. O `DOMAIN_SUFFIX` das
versões anteriores continua aceito, como um domínio coringa.

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

A ordem de prioridade é:

1. `--cert/--key` informados no comando (num terminal, o `app create`
   também pergunta se você quer informar um quando nenhum cobre os
   domínios);
2. `SSL_CERT_FILE` do `/etc/deploy-tools.conf`;
3. o certificado cadastrado para o domínio coringa (`BASE_CERT`), se cobrir
   os domínios;
4. a busca automática.

**Sem certificado, o site fica só em HTTP**: o vhost tem apenas o
`<VirtualHost *:80>`, sem redirecionamento. O `app status` mostra o endereço
`http://` e avisa. Quando o certificado existir, o `app cert` (ou adicionar
um domínio que ele cubra) converte o vhost para HTTPS: o bloco da porta 80
vira um redirecionamento, e o conteúdo dele, inclusive edições feitas à
mão, passa para um novo bloco `*:443` com o certificado.

Depois de emitir ou renovar um certificado:

```bash
sudo certbot certonly --webroot -w /var/www/minha-api/current/public -d api.example.com
sudo deploy-tools app cert minha-api      # procura de novo e troca no site
sudo deploy-tools server certs            # lista o que foi encontrado, com domínios e validade
```

O `app cert` nunca tira o HTTPS de um site. Se nada cobrir os domínios, o
certificado atual é mantido e o comando avisa. Num terminal (ou na TUI), ele
oferece informar os arquivos do certificado.

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

## Domínios coringa

Cadastre os domínios base, como `vinicgobbi.dev.br`, e cada app novo ganha
`<subdomínio>.<domínio>` sem precisar de `--domain`:

```bash
sudo deploy-tools server domain add vinicgobbi.dev.br
sudo deploy-tools server domain add example.com --cert /etc/ssl/certs/wild.crt --key /etc/ssl/private/wild.key
deploy-tools server domain list
sudo deploy-tools server domain remove example.com     # os sites que já usam não mudam
```

Ao cadastrar, o comando diz se há um certificado `*.<domínio>` no servidor.
Se não houver, num terminal (ou na TUI, em "Domínios coringa"), ele pergunta
se você quer informar um. O certificado informado é validado: a chave
precisa ser dele, ele precisa estar dentro da validade e ser coringa do
domínio. Depois fica gravado em `BASE_CERT`. Com ele, todo app novo no
domínio já sai com HTTPS.

Ao criar um app:

- com um único domínio coringa, ele é usado direto (`api` →
  `api.vinicgobbi.dev.br`);
- com vários, a CLI (num terminal) e a TUI perguntam qual usar e qual
  subdomínio. O padrão é o nome do app, com `_` trocado por `-`. Também dá
  para adicionar outros domínios, como o `www.`;
- em scripts, use `--base vinicgobbi.dev.br [--sub admin]`.

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
deploy-tools app create <app> --type laravel|symfony|react|angular|static [-d dominio]... | [--base b [--sub s]] [--cert c --key k [--chain b]] [--migrate-on-deploy] [--enable]
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
deploy-tools key generate [--out arquivo | --print-only] [--comment c]
deploy-tools key list
deploy-tools key secrets [--host h] [--repo dono/repo | --org org]
deploy-tools server install
deploy-tools server setup [--host h] [--generate-key [--key-out arquivo] | --key arquivo.pub]
deploy-tools server certs
deploy-tools server domain list|add|remove [domínio]... [--cert c --key k [--chain b]]
deploy-tools server status                      # instalado ou rodando solto?
deploy-tools update [--check]                   # atualiza para a última release
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
sudo deploy-tools app create docs --type static --base vinicgobbi.dev.br   # docs.vinicgobbi.dev.br
```

O comando cria:

- `/var/www/<app>/{releases,shared}` com dono `deploy:www-data` e modo 2775.
  O setgid faz os arquivos herdarem o grupo `www-data`, então o Apache
  consegue ler.
- O `.deploy.conf`.
- O vhost em `sites-available/`. Com certificado (escolhido como em
  [Certificados TLS](#certificados-tls)), porta 443 com SSL mais uma porta
  80 que redireciona para HTTPS; sem certificado, só a porta 80.

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

| | Laravel | Symfony | React / Angular | Static |
|---|---|---|---|---|
| `DocumentRoot` | `current/public` (PHP via mod_php, sem FPM) | `current/public` (PHP via mod_php) | `current` | `current` |
| Build esperado no tarball | checkout com `vendor/` já instalado | checkout com `vendor/` (sem dev) e assets públicos já gerados; sem `var/` | conteúdo de `dist/` (Vite) ou da saída do `ng build` | qualquer site já buildado (ex.: mkdocs `site/`) |
| Rotas | `.htaccess` do próprio Laravel | `FallbackResource /index.php` no vhost (não depende do `apache-pack`) | `RewriteRule` de SPA no vhost (o roteador do front funciona em refresh direto) | nenhuma regra especial |
| `shared/` | `.env` (vazio, você preenche) + `storage/` | `.env.local` (criado com `APP_ENV=prod`) + `var/log` | não usa | não usa |
| Hooks no deploy | `storage:link`, `config:cache`, `route:cache`, `view:cache` (+ `migrate --force` opcional) | `cache:clear --env=prod` (+ `doctrine:migrations:migrate` opcional) | nenhum | nenhum |

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
     symlink;
   - para Symfony, o mesmo acontece com `.env.local` e `var/log`.
5. Grava o `.deploy.conf`.

O vhost é detectado em `sites-enabled/` pelo `DocumentRoot`. Se houver mais de
um, ou nenhum, indique o arquivo com `--vhost`. A partir daí, o próximo push
já cria a release seguinte normalmente.

## `.deploy.conf` e hooks de PHP

`/var/www/<app>/.deploy.conf` é um arquivo `KEY=value` gravado pelo
`app create`, `app migrate` e `app config`:

```
TYPE=laravel
MIGRATE=no
```

O `LARAVEL_MIGRATE` das versões anteriores continua sendo lido. Em apps
PHP, todo deploy roda os hooks dentro da release nova, **antes** de trocar o
`current`. Com `TYPE=laravel`:

```
php artisan storage:link
php artisan config:cache
php artisan route:cache
php artisan view:cache
php artisan migrate --force      # só com MIGRATE=yes
```

Com `TYPE=symfony`:

```
php bin/console cache:clear --env=prod --no-debug
php bin/console doctrine:migrations:migrate --no-interaction --allow-no-migration --env=prod --no-debug   # só com MIGRATE=yes
```

No Symfony, o cache (`var/cache`) é de cada release: é gerado no caminho
dela e depois liberado para escrita do grupo `www-data`, porque o Apache
grava os cache pools em tempo de execução. Só o `var/log` é compartilhado.

Se qualquer hook falhar, a release nova é apagada e o `current` continua na
anterior, ou seja, o site não muda. O mesmo vale para um tarball corrompido.
Os comandos são fixos no binário: nada do tarball é executado como script.

Para apps sem `.deploy.conf` (por exemplo, migrados), ligue os hooks com:

```bash
sudo deploy-tools app config sistema --type laravel     # ou symfony; sem --type: detecta pelo current/
sudo deploy-tools app config sistema --migrate-on-deploy
```

## Por que existe `shared/`

Cada deploy é uma pasta nova em `releases/`. Para Laravel, isso apagaria
`.env` e `storage/` (uploads, sessions, cache, logs) a cada deploy; para
Symfony, o `.env.local` e os logs. Para evitar isso, tudo o que está em
`/var/www/<app>/shared/` é symlinkado para dentro da release recém-extraída,
dotfiles incluídos, antes de trocar o `current`. No Symfony, `shared/var-log`
vira `var/log` dentro da release.

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
| `DEPLOY_HOST_KEY` | Linhas `known_hosts` do servidor (fixa o host key e evita MITM sem precisar de `StrictHostKeyChecking=no`) |

Para ver esses valores a qualquer hora, rode no servidor:

```bash
deploy-tools key secrets                         # ou o item "Secrets do GitHub" na interface
deploy-tools key secrets --repo acme/site        # já com os comandos gh secret set do repositório
deploy-tools key secrets --org acme              # secrets da organização, para vários repositórios
deploy-tools key secrets --copy                  # copia os comandos gh para a área de transferência
deploy-tools key secrets --gh-only               # só os comandos, sem formatação
deploy-tools key secrets --script > github-secrets.sh   # script: sh github-secrets.sh dono/repo [chave]
```

Na TUI, a seleção com o mouse pega pedaços do menu ao lado. Por isso, depois
dos secrets (e da chave privada, quando ela é gerada no servidor), aparece a
opção de **copiar**. A cópia usa OSC 52, uma sequência que pede ao *seu*
terminal para guardar o texto, e funciona por SSH. Se o seu terminal não
aceitar (por exemplo, tmux sem `set-clipboard on`), use **Salvar num
arquivo**: o script vai para `github-secrets.sh`, na pasta atual (modo 600),
para você rodar depois de sair com `sh github-secrets.sh dono/repo`, onde o
`gh` estiver logado.

O comando lê `DEPLOY_HOST_KEY` das host keys do próprio servidor
(`/etc/ssh/ssh_host_*_key.pub`), mostra as chaves da CI autorizadas (para
conferir com `ssh-keygen -lf ci_deploy_key` na sua máquina) e os comandos
`gh secret set` equivalentes. Com eles, a chave privada vai do arquivo direto
para o GitHub, sem passar pela tela. A chave privada nunca fica no servidor,
então o comando não tem como mostrá-la. Se ela se perdeu, gere outra com
`sudo deploy-tools key generate` (e remova a antiga do `authorized_keys`).

> **Não compartilhe essa saída** (chat, ticket, e-mail, print de tela). Junto
> com a chave privada, ela permite publicar em todos os apps do servidor.

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

- **Tipo:** `artisan` indica Laravel; `bin/console` com
  `symfony/framework-bundle` no `composer.json` indica Symfony;
  `angular.json` indica Angular; Vite/React indica React; e `mkdocs.yml`
  indica estático com mkdocs. Qualquer outro caso vira uma pasta pronta.
- **Angular:** a pasta publicada vem do `angular.json` (o `outputPath` do
  projeto, mais `browser/` no builder `application` do Angular 17+).
- **Symfony:** AssetMapper (`symfony/asset-mapper`, compila com
  `asset-map:compile`) e build Node (Encore/Vite, quando há script `build`).
  O `assets:install` sempre roda. O `var/` e o `.env.local` ficam fora do
  pacote, e o `.env` (os padrões do Symfony) vai junto.
- **Commitizen:** presença de `cz.toml`, `.cz.toml` ou `[tool.commitizen]`
  no `pyproject.toml`.
- **Node:** pnpm ou npm, lint, os `VITE_*` do `.env.example` e o registry npm
  do GitHub Packages (`npm.pkg.github.com` no `.npmrc`).
- **PHP:** versão (do `composer.json`) e Pint.

Num projeto que já faz deploy, o `APP_NAME` existente é mantido. O workflow
gerado é dividido em jobs, que aparecem como etapas no GitHub Actions:

```
validate ─▶ release ─▶ build ─▶ deploy
```

- **validate**: lint, Pint e `php -l`. Só existe quando o projeto tem algo
  a validar.
- **release**: bump com Commitizen e release no GitHub. Só quando o projeto
  usa Commitizen; a versão segue para os próximos jobs.
- **build**: monta o `release.tar.gz`, a partir da tag do bump quando houve
  um. O pacote segue para o deploy como artefato.
- **deploy**: baixa o artefato, publica no servidor e **apaga o artefato**.
  Se o deploy falhar, o artefato expira em 1 dia.

Um `concurrency` garante um deploy por vez na branch. Como cada job roda
numa máquina nova, as dependências são instaladas no validate e de novo no
build.

O workflow também tem:

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
