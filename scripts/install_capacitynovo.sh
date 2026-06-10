#!/usr/bin/env bash
# install_capacitynovo.sh — instala a Capacity NSX de ponta a ponta a partir
# do seu laptop. Faz tudo:
#   1. Build do binário Linux a partir do repo local.
#   2. Copia binário + deploy_capacitynovo.sh pro host destino.
#   3. Executa deploy_capacitynovo.sh remoto (com backup + rollback automático).
#   4. Traz de volta o log do deploy.
#
# REQUISITOS NO LAPTOP:
#   - Go 1.23+ (para o build)
#   - sshpass (apt install sshpass / dnf install sshpass)
#   - Acesso SSH ao host destino (admin@HOST com a senha do NSX manager)
#
# USO RÁPIDO (TESP3):
#   ./install_capacitynovo.sh tesp3
#
# USO EXPLÍCITO (qualquer host):
#   HOST=10.114.36.200 SITE=TESP6 ./install_capacitynovo.sh
#
# DRY-RUN (só simula — não muda nada no destino):
#   ./install_capacitynovo.sh tesp3 --dry-run
#
# OPÇÕES via env:
#   HOST           IP/hostname do destino (override)
#   SITE           rótulo do site (default = nome do shortcut, ex. TESP3)
#   SSH_USER       usuário SSH (default admin)
#   SSH_PASS       senha SSH (default = senha NSX padrão)
#   GO_BUILD       0 pra pular build (usa /tmp/nsx-collector existente)
#   SKIP_HEALTH    1 pra pular health check remoto (NÃO usar em prod)
#   HEALTH_TIMEOUT segundos para o health check remoto (default 60)
set -euo pipefail

# ---------- atalhos por site ---------------------------------------------
declare -A SITE_HOSTS=(
  [tece]=10.114.32.200      # ajuste conforme necessário
  [tesp2]=10.114.34.200
  [tesp3]=10.100.29.200
  [tesp4]=10.114.35.200
  [tesp5]=10.114.36.100
  [tesp6]=10.114.36.200
  [tesp7]=10.114.37.200
)

# Aceita 1º arg como atalho de site (tesp3, tesp6 etc.) e flag --dry-run em qualquer posição.
SHORTCUT=""
DRY_RUN=0
for a in "$@"; do
  case "$a" in
    --dry-run|-n) DRY_RUN=1 ;;
    -*) echo "flag desconhecida: $a" >&2; exit 1 ;;
    *) [[ -z "$SHORTCUT" ]] && SHORTCUT="${a,,}" ;;
  esac
done

if [[ -n "$SHORTCUT" ]]; then
  if [[ -n "${SITE_HOSTS[$SHORTCUT]:-}" ]]; then
    : "${HOST:=${SITE_HOSTS[$SHORTCUT]}}"
    : "${SITE:=${SHORTCUT^^}}"
  else
    echo "atalho '$SHORTCUT' não existe. Sites conhecidos: ${!SITE_HOSTS[*]}" >&2
    echo "Use HOST=... SITE=... ./install_capacitynovo.sh" >&2
    exit 1
  fi
fi

: "${HOST:?HOST não definido — use atalho (ex: ./install_capacitynovo.sh tesp3) ou HOST=10.x.x.x}"
: "${SITE:=${HOST}}"
: "${SSH_USER:=admin}"
: "${SSH_PASS:=nSx--T@!@dm!n#nsxT@2!}"
: "${GO_BUILD:=1}"
: "${SKIP_HEALTH:=0}"
: "${HEALTH_TIMEOUT:=60}"

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BIN_LOCAL="/tmp/nsx-collector"
SCRIPT_LOCAL="${REPO_DIR}/scripts/deploy_capacitynovo.sh"
TS=$(date -u +%Y%m%dT%H%M%SZ)
LOG_LOCAL="/tmp/install_capacitynovo-${SITE}-${TS}.log"

say()  { printf '[%s] ==> %s\n' "$(date -u +%FT%TZ)" "$*" | tee -a "$LOG_LOCAL" >&2; }
warn() { printf '[%s] WARN: %s\n' "$(date -u +%FT%TZ)" "$*" | tee -a "$LOG_LOCAL" >&2; }
die()  { printf '[%s] ERRO: %s\n' "$(date -u +%FT%TZ)" "$*" | tee -a "$LOG_LOCAL" >&2; exit "${2:-1}"; }

# ---------- 0. checks locais ---------------------------------------------
say "Instalação Capacity NSX → ${SITE} (${HOST})  [dry-run=${DRY_RUN}]"
say "log local: ${LOG_LOCAL}"

command -v sshpass >/dev/null 2>&1 \
  || die "sshpass não instalado. Rode: sudo apt install sshpass  ou  sudo dnf install sshpass"
command -v ssh >/dev/null 2>&1 || die "ssh não instalado."
command -v scp >/dev/null 2>&1 || die "scp não instalado."

[[ -f "$SCRIPT_LOCAL" ]] || die "script de deploy não encontrado: $SCRIPT_LOCAL"

# ---------- 1. build local -----------------------------------------------
if [[ $GO_BUILD -eq 1 ]]; then
  command -v go >/dev/null 2>&1 || die "go não instalado. Instale Go 1.23+ ou rode com GO_BUILD=0 se já tem o binário em $BIN_LOCAL"
  say "Build do binário Linux amd64…"
  ( cd "$REPO_DIR" && \
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
      -o "$BIN_LOCAL" ./cmd/ )
  SHA=$(sha256sum "$BIN_LOCAL" | awk '{print $1}')
  SIZE=$(du -h "$BIN_LOCAL" | awk '{print $1}')
  say "Binário pronto: ${BIN_LOCAL} (${SIZE}, sha256 ${SHA:0:16}…)"
else
  [[ -f "$BIN_LOCAL" ]] || die "GO_BUILD=0 mas $BIN_LOCAL não existe."
  say "Pulando build (GO_BUILD=0). Usando $BIN_LOCAL"
fi

# ---------- 2. ssh helpers -----------------------------------------------
SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR)
ssh_run() {
  sshpass -p "$SSH_PASS" ssh "${SSH_OPTS[@]}" "${SSH_USER}@${HOST}" "$@"
}
scp_to() {
  sshpass -p "$SSH_PASS" scp "${SSH_OPTS[@]}" "$@" "${SSH_USER}@${HOST}:/tmp/"
}

say "Teste de conexão SSH em ${SSH_USER}@${HOST}…"
ssh_run "echo ok && uname -a" >/dev/null 2>&1 \
  || die "falha de SSH em ${SSH_USER}@${HOST}. Cheque rede/credenciais."

# Confirma se o service nsx-collector já existe — instalação nova vs upgrade.
EXISTING=$(ssh_run "systemctl is-active nsx-collector 2>/dev/null || echo 'absent'" || true)
say "Estado atual do service: ${EXISTING}"
[[ "$EXISTING" == "absent" ]] && warn "service nsx-collector ainda não instalado — este é um INSTALL FROM SCRATCH. O deploy script vai falhar se /home/nsx_collector/configs não existir. Considere rodar setup.sh primeiro."

# ---------- 3. copiar binário + script para /tmp do destino --------------
say "Copiando binário e script para ${HOST}:/tmp/…"
scp_to "$BIN_LOCAL" "$SCRIPT_LOCAL"
ssh_run "chmod +x /tmp/nsx-collector /tmp/deploy_capacitynovo.sh"
say "Arquivos no destino:"
ssh_run "ls -l /tmp/nsx-collector /tmp/deploy_capacitynovo.sh" | tee -a "$LOG_LOCAL"

# ---------- 4. roda o deploy remoto --------------------------------------
DEPLOY_ENV="BIN_SOURCE=/tmp/nsx-collector HEALTH_TIMEOUT=${HEALTH_TIMEOUT}"
[[ $SKIP_HEALTH -eq 1 ]] && DEPLOY_ENV="${DEPLOY_ENV} SKIP_HEALTH=1"
[[ $DRY_RUN -eq 1 ]]   && DEPLOY_ENV="${DEPLOY_ENV} DRY_RUN=1"

say "Executando deploy_capacitynovo.sh no destino (envs: ${DEPLOY_ENV})…"
if ssh_run "sudo ${DEPLOY_ENV} bash /tmp/deploy_capacitynovo.sh" 2>&1 | tee -a "$LOG_LOCAL"; then
  RC=0
else
  RC=${PIPESTATUS[0]}
fi

if [[ $RC -ne 0 ]]; then
  warn "deploy remoto falhou (rc=$RC). O script remoto faz rollback automático em rc=5 (health-check)."
  warn "Veja log no destino:  sudo ls -lt /home/nsx_collector/logs/deploy_capacitynovo-*.log | head -1"
  exit "$RC"
fi

# ---------- 5. estado pós-deploy + dica de logs --------------------------
say "Estado pós-deploy em ${HOST}:"
ssh_run "systemctl is-active nsx-collector; systemctl is-enabled nsx-collector 2>/dev/null || true" | tee -a "$LOG_LOCAL"
say "Últimas 10 linhas do journal:"
ssh_run "journalctl -u nsx-collector -n 10 --no-pager 2>/dev/null || true" | tee -a "$LOG_LOCAL"
say "Métricas do collector (filtro nsx_collector_*):"
ssh_run "curl -fsS http://127.0.0.1:9101/metrics 2>/dev/null | grep -E '^nsx_collector_(collect_cycles|lb_credits|t1_known)' | head -20" \
  | tee -a "$LOG_LOCAL" || warn "Não consegui pegar /metrics — pode estar inicializando."

say "INSTALAÇÃO OK em ${SITE} (${HOST})."
say "Log local: ${LOG_LOCAL}"
say ""
say "Próximos passos:"
say "  1. Aguarde 1 slow cycle (5min) e confira em network-grafana → dashboard 'Capacity NSX'."
say "  2. Para ligar o Slack bot:"
say "     ssh ${SSH_USER}@${HOST}"
say "     sudo vi /home/nsx_collector/configs/config.yaml   # t1_watch.enabled: true"
say "     sudo systemctl restart nsx-collector              # primeiro ciclo só baseline."
say "  3. Para reverter:"
say "     ssh ${SSH_USER}@${HOST}"
say "     sudo ls /home/nsx_collector/backups/capacity-novo/"
say "     sudo ROLLBACK_TO=/home/nsx_collector/backups/capacity-novo/<TS> /tmp/deploy_capacitynovo.sh --rollback"
