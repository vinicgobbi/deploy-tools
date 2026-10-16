#!/usr/bin/env bash
# Envia o tarball ao deploy-ci do servidor (chamado pelo action.yml).
#
# Falha de conexão SSH (código 255: o servidor não respondeu ou a conexão
# caiu) é tentada de novo, com espera crescente. Antes de cada nova
# tentativa, consulta o servidor: se a versão enviada já é a atual (a
# conexão caiu depois de o deploy terminar), conta como sucesso. Cada
# tentativa usa um release id novo, então nunca colide com a anterior.
# Recusas do próprio deploy-ci (app inexistente, hook que falhou...) não
# são repetidas: tentar de novo não as resolveria.
set -euo pipefail

: "${APP:?}" "${ARCHIVE:?}" "${DEPLOY_HOST:?}" "${DEPLOY_USER:?}" "${DEPLOY_SSH_KEY:?}" "${DEPLOY_HOST_KEY:?}"
ATTEMPTS="${ATTEMPTS:-6}"
CONNECT_TIMEOUT="${CONNECT_TIMEOUT:-20}"
delay="${RETRY_DELAY:-10}" # espera antes da 2ª tentativa; dobra a cada uma, até 120s

[ -f "$ARCHIVE" ] || { echo "::error::arquivo não encontrado: $ARCHIVE"; exit 1; }
[[ "$ATTEMPTS" =~ ^[1-9][0-9]*$ ]] || { echo "::error::attempts inválido: $ATTEMPTS"; exit 1; }

# O id começa com timestamp: o deploy-ci ordena e poda pelo nome.
suffix="${SUFFIX:-${GITHUB_SHA::7}}"

# Chave e known_hosts num diretório temporário, apagado no fim.
dir="$(mktemp -d)"
trap 'rm -rf "$dir"' EXIT
printf '%s\n' "$DEPLOY_SSH_KEY" > "$dir/key"
chmod 600 "$dir/key"
printf '%s\n' "$DEPLOY_HOST_KEY" > "$dir/known_hosts"

remote() {
  ssh -i "$dir/key" \
      -o IdentitiesOnly=yes \
      -o UserKnownHostsFile="$dir/known_hosts" \
      -o StrictHostKeyChecking=yes \
      -o ConnectTimeout="$CONNECT_TIMEOUT" \
      -o ServerAliveInterval=15 \
      -o ServerAliveCountMax=4 \
      "$DEPLOY_USER@$DEPLOY_HOST" "$@"
}

for attempt in $(seq 1 "$ATTEMPTS"); do
  RELEASE_ID="$(date -u +%Y%m%d%H%M%S)-$suffix"
  [[ "$RELEASE_ID" =~ ^[a-zA-Z0-9_.-]+$ ]] || { echo "::error::release id inválido: $RELEASE_ID"; exit 1; }

  set +e
  remote "deploy $APP $RELEASE_ID" < "$ARCHIVE"
  code=$?
  set -e

  if [ "$code" -eq 0 ]; then
    break
  fi
  if [ "$code" -ne 255 ]; then
    echo "::error::o servidor recusou o deploy (código $code) — veja a mensagem acima"
    exit "$code"
  fi
  # A conexão pode ter caído depois de o deploy terminar no servidor.
  if remote "list $APP" 2>/dev/null | grep -qxF "* $RELEASE_ID (current)"; then
    echo "A conexão caiu, mas a versão $RELEASE_ID chegou e está no ar."
    break
  fi
  if [ "$attempt" -eq "$ATTEMPTS" ]; then
    echo "::error::sem conexão SSH com $DEPLOY_HOST depois de $ATTEMPTS tentativas"
    exit 255
  fi
  echo "::warning::falha na conexão SSH com $DEPLOY_HOST (tentativa $attempt de $ATTEMPTS); tentando de novo em ${delay}s"
  sleep "$delay"
  delay=$(( delay * 2 > 120 ? 120 : delay * 2 ))
done

echo "release-id=$RELEASE_ID" >> "${GITHUB_OUTPUT:-/dev/null}"
echo "Publicado $APP -> $RELEASE_ID"
