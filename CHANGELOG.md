## v0.8.1 (2026-10-15)

### Fix

- **cli**: desfaz o alias www. automático

## v0.8.0 (2026-10-15)

### Feat

- **cli**: todo domínio cadastrado ganha o alias www.

## v0.7.0 (2026-10-15)

### Feat

- **cli**: pergunta o DEPLOY_HOST no setup e gera script de secrets por repositório

## v0.6.0 (2026-10-15)

### Feat

- **cli**: grava a chave da CI gerada na pasta onde o deploy-tools foi chamado

## v0.5.0 (2026-10-15)

### Feat

- **cli**: copiar comandos gh e chave privada sem selecionar na tela

## v0.4.0 (2026-10-15)

### Feat

- **cli**: gera a chave da CI no servidor (key generate, server setup --generate-key)

## v0.3.0 (2026-10-15)

### Feat

- suporte a Symfony e Angular no deploy-tools e no deploy-dev
- **tls**: certificado por domínio coringa e site só em HTTP sem certificado
- **cli**: domínios coringa para o endereço dos apps

## v0.2.0 (2026-10-14)

### Feat

- **cli**: mostra se o binário está instalado e como ficam os secrets do GitHub

## v0.1.0 (2026-10-13)

Primeira versão da variante genérica para GitHub.

### Feat

- **ci**: deploy pelo GitHub Actions; a action de deploy é usada direto com `uses: vinicgobbi/deploy-tools@<sha>` e o versionamento usa só o `GITHUB_TOKEN`
- **cli**: forced command passa a ser `/usr/local/bin/deploy-ci`
- **cli**: vários domínios por site (`app create -d a -d b`, `app domain list|add|remove`)
- **tls**: busca automática de certificados (Let's Encrypt, /etc/ssl, /etc/pki/tls, pastas do Apache e `SSL_SEARCH_DIRS`), validando domínios, validade e chave; `app cert` e `server certs`
- **config**: `/etc/deploy-tools.conf` com `DOMAIN_SUFFIX`, `SSL_CERT_*` e `SSL_SEARCH_DIRS`
- **deploy-dev**: gera `.github/workflows/deploy.yml`, com release no GitHub e registry npm do GitHub Packages
