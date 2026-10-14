## v0.1.0 (2026-10-13)

Primeira versão da variante genérica para GitHub.

### Feat

- **ci**: deploy pelo GitHub Actions; a action de deploy é usada direto com `uses: vinicgobbi/deploy-tools@<sha>` e o versionamento usa só o `GITHUB_TOKEN`
- **cli**: forced command passa a ser `/usr/local/bin/deploy-ci`
- **cli**: vários domínios por site (`app create -d a -d b`, `app domain list|add|remove`)
- **tls**: busca automática de certificados (Let's Encrypt, /etc/ssl, /etc/pki/tls, pastas do Apache e `SSL_SEARCH_DIRS`), validando domínios, validade e chave; `app cert` e `server certs`
- **config**: `/etc/deploy-tools.conf` com `DOMAIN_SUFFIX`, `SSL_CERT_*` e `SSL_SEARCH_DIRS`
- **deploy-dev**: gera `.github/workflows/deploy.yml`, com release no GitHub e registry npm do GitHub Packages
