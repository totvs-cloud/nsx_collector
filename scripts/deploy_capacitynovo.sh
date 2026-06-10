#!/usr/bin/env bash
# deploy_capacitynovo.sh — instala a nova versão do nsx-collector (Capacity NSX)
# clonando o repo, buildando localmente e atualizando com backup + rollback.
#
# DESENHO (executar como root na dev-redes do site):
#   0. Garante git + go instalados (via dnf no OL/RHEL ou apt no Ubuntu).
#   0b. Clona/atualiza o repo em SRC_DIR (default /opt/nsx-collector-src) e
#       compila o binário em $SRC_DIR/bin/nsx-collector. NADA é tocado no
#       diretório de produção até o build estar OK.
#   1. Pré-checks (systemd, binário gerado, dirs, espaço em disco, distro).
#   2. Backup atômico de tudo que pode mudar: binário, configs, .env,
#      unit file, state/, sob /home/nsx_collector/backups/capacity-novo/<ts>/.
#   3. Patch idempotente em config.yaml: adiciona blocos t1_watch e capacity
#      SOMENTE se ainda não existirem. interface_speed_overrides e managers
#      do site são preservados verbatim.
#   4. mkdir do state_dir do t1watch (se necessário) com perms corretas.
#   5. systemctl stop, swap atômico do binário, systemctl start.
#   6. Health check: /metrics responde + nsx_collector_collect_cycles_total
#      cresce dentro do timeout. Se falhar → rollback automático.
#   7. systemctl enable (memória: sites antigos ficaram sem enable e não
#      voltavam após reboot).
#   8. Prune de backups antigos (mantém últimos KEEP_BACKUPS).
#
# USO (caminho feliz):
#   sudo ./deploy_capacitynovo.sh                          # clona + builda + deploya
#   sudo DRY_RUN=1 ./deploy_capacitynovo.sh                # simula sem mudar
#   sudo REPO_REF=v1.2.3 ./deploy_capacitynovo.sh          # pina tag/branch/commit
#   sudo HEALTH_TIMEOUT=120 ./deploy_capacitynovo.sh       # mais paciência
#   sudo SKIP_HEALTH=1 ./deploy_capacitynovo.sh            # NUNCA em prod
#   sudo SKIP_BUILD=1 BIN_SOURCE=/tmp/x ./deploy_capacitynovo.sh  # usa binário pronto
#   sudo ROLLBACK_TO=/path/to/backup-dir ./deploy_capacitynovo.sh --rollback
#
# SEGURANÇA:
#   - Toda a fase clone+build acontece em SRC_DIR, ISOLADA de $INSTALL_DIR.
#   - Se git ou build falhar, NADA do install em produção é tocado.
#   - Backup atômico antes do swap. Health-check com auto-rollback se falhar.
#   - REPO_URL fixo (totvs-cloud/nsx_collector) — override só via env.
#
# Exit codes: 0=ok, 1=user error, 2=preflight, 3=backup, 4=install,
#             5=health-check failed (rolled back), 6=rollback failed,
#             7=build failed (nada foi tocado em produção).
set -euo pipefail
shopt -s lastpipe

# ---------- Defaults configuráveis via env ---------------------------------
# Build / clone
REPO_URL=${REPO_URL:-https://github.com/totvs-cloud/nsx_collector.git}
REPO_REF=${REPO_REF:-master}             # branch, tag ou commit
SRC_DIR=${SRC_DIR:-/opt/nsx-collector-src}
SKIP_BUILD=${SKIP_BUILD:-0}              # 1 = usar BIN_SOURCE existente, pula clone+build
GO_MIN_VERSION=${GO_MIN_VERSION:-1.23}

# Install / runtime
BIN_SOURCE=${BIN_SOURCE:-}               # default vazio → vem do build em SRC_DIR
INSTALL_DIR=${INSTALL_DIR:-/home/nsx_collector}
SERVICE=${SERVICE:-nsx-collector}
SERVICE_USER=${SERVICE_USER:-nsx_collector}
BACKUP_ROOT=${BACKUP_ROOT:-/home/nsx_collector/backups/capacity-novo}
METRICS_URL=${METRICS_URL:-http://127.0.0.1:9101/metrics}
DRY_RUN=${DRY_RUN:-0}
SKIP_HEALTH=${SKIP_HEALTH:-0}
HEALTH_TIMEOUT=${HEALTH_TIMEOUT:-300}     # segundos para validar /metrics + ciclos
                                          # default 300 porque o 1o cycle com a
                                          # coleta nova (LB credits + Policy T0/T1
                                          # + segments paginados) leva ~2-3 min em
                                          # sites grandes (TESP2 = 2272 T1s).
KEEP_BACKUPS=${KEEP_BACKUPS:-5}
ROLLBACK_TO=${ROLLBACK_TO:-}              # quando --rollback é usado

TS=$(date -u +%Y%m%dT%H%M%SZ)
BACKUP_DIR="${BACKUP_ROOT}/${TS}"
LOG_DIR="${INSTALL_DIR}/logs"
LOG_FILE="${LOG_DIR}/deploy_capacitynovo-${TS}.log"

# ---------- helpers --------------------------------------------------------
log()   { printf '[%s] %s\n' "$(date -u +%FT%TZ)" "$*" | tee -a "$LOG_FILE" >&2; }
fatal() { log "FATAL: $*"; exit "${2:-1}"; }
say()   { log "==> $*"; }

# run executa um comando ou só loga (DRY_RUN=1).
run() {
  if [[ $DRY_RUN -eq 1 ]]; then
    log "DRY-RUN: $*"
    return 0
  fi
  log "exec: $*"
  "$@"
}

cleanup_on_err() {
  local rc=$?
  if [[ $rc -ne 0 && -n "${BACKUP_DIR:-}" && -d "${BACKUP_DIR}" ]]; then
    log "ERROR (rc=$rc) — backup preservado em: ${BACKUP_DIR}"
    log "Para reverter manualmente:  sudo ROLLBACK_TO=${BACKUP_DIR} $0 --rollback"
  fi
}
trap cleanup_on_err EXIT

# ---------- 0. parse args --------------------------------------------------
ACTION="deploy"
if [[ ${1:-} == "--rollback" ]]; then
  ACTION="rollback"
fi

# ---------- 1. require root + log dir --------------------------------------
if [[ $EUID -ne 0 ]]; then
  echo "ERRO: rode com sudo/root (precisa para systemctl e copiar em ${INSTALL_DIR})." >&2
  exit 1
fi
mkdir -p "$LOG_DIR"
chown -R "${SERVICE_USER}:${SERVICE_USER}" "$LOG_DIR" 2>/dev/null || true
touch "$LOG_FILE"

say "deploy_capacitynovo iniciado (action=${ACTION}, dry_run=${DRY_RUN})"
say "log: ${LOG_FILE}"

# ---------- ROLLBACK explícito ---------------------------------------------
if [[ $ACTION == "rollback" ]]; then
  [[ -z "$ROLLBACK_TO" ]] && fatal "use: sudo ROLLBACK_TO=/path/to/backup $0 --rollback" 1
  [[ ! -d "$ROLLBACK_TO" ]] && fatal "diretório de backup não existe: $ROLLBACK_TO" 1
  say "Rollback a partir de: $ROLLBACK_TO"
  run systemctl stop "$SERVICE" || true
  [[ -f "${ROLLBACK_TO}/nsx-collector" ]] && run cp -p "${ROLLBACK_TO}/nsx-collector" "${INSTALL_DIR}/nsx-collector"
  if [[ -d "${ROLLBACK_TO}/configs" ]]; then
    run rm -rf "${INSTALL_DIR}/configs"
    run cp -rp "${ROLLBACK_TO}/configs" "${INSTALL_DIR}/configs"
  fi
  [[ -f "${ROLLBACK_TO}/.env" ]] && run cp -p "${ROLLBACK_TO}/.env" "${INSTALL_DIR}/.env"
  [[ -f "${ROLLBACK_TO}/nsx-collector.service" ]] && run cp -p "${ROLLBACK_TO}/nsx-collector.service" /etc/systemd/system/nsx-collector.service
  run systemctl daemon-reload
  run systemctl start "$SERVICE"
  say "Rollback aplicado. Verifique:  systemctl status $SERVICE  |  journalctl -u $SERVICE -n 80"
  trap - EXIT
  exit 0
fi

# ---------- 1b. ensure git + go ------------------------------------------
ensure_pkg() {
  local pkg=$1
  if command -v "$pkg" >/dev/null 2>&1; then return 0; fi
  say "Instalando $pkg…"
  if command -v dnf >/dev/null 2>&1; then
    run dnf install -y "$pkg"
  elif command -v yum >/dev/null 2>&1; then
    run yum install -y "$pkg"
  elif command -v apt-get >/dev/null 2>&1; then
    run apt-get update -y
    run apt-get install -y "$pkg"
  else
    fatal "não consegui detectar gerenciador de pacotes (dnf/yum/apt) para instalar $pkg" 2
  fi
}

# Compara duas versões "1.23.4 vs 1.23" — retorna 0 se atual >= mínima.
version_ge() {
  local a=$1 b=$2
  # converte cada parte (max 3) em decimal e compara como string padded
  local IFS=.
  read -ra A <<<"${a//+/}"
  read -ra B <<<"${b//+/}"
  for i in 0 1 2; do
    local av=${A[$i]:-0} bv=${B[$i]:-0}
    av=${av%%[!0-9]*}; bv=${bv%%[!0-9]*}
    av=${av:-0}; bv=${bv:-0}
    (( 10#$av > 10#$bv )) && return 0
    (( 10#$av < 10#$bv )) && return 1
  done
  return 0
}

if [[ $SKIP_BUILD -eq 1 ]]; then
  say "SKIP_BUILD=1 — pulando clone+build; usando BIN_SOURCE=${BIN_SOURCE:-?}"
  [[ -z "$BIN_SOURCE" ]] && fatal "SKIP_BUILD=1 exige BIN_SOURCE setado" 1
else
  ensure_pkg git
  ensure_pkg gcc      # CGO pode precisar; nosso build é CGO=0 mas algumas distros forçam

  if ! command -v go >/dev/null 2>&1; then
    say "Instalando golang…"
    ensure_pkg golang
  fi
  GO_VERSION=$(go version 2>/dev/null | awk '{print $3}' | sed 's/^go//')
  say "Go detectado: ${GO_VERSION:-desconhecido} (mínimo ${GO_MIN_VERSION})"
  if [[ -n "$GO_VERSION" ]] && ! version_ge "$GO_VERSION" "$GO_MIN_VERSION"; then
    fatal "go ${GO_VERSION} < mínimo ${GO_MIN_VERSION}. Atualize: dnf module install go-toolset:rhel9 ou baixe de go.dev/dl/" 2
  fi

  # ---------- 1c. clone ou pull do repo ----------------------------------
  say "Repositório: ${REPO_URL}  ref=${REPO_REF}  →  ${SRC_DIR}"
  run mkdir -p "$(dirname "$SRC_DIR")"
  if [[ -d "$SRC_DIR/.git" ]]; then
    say "Repo já existe — atualizando."
    run git -C "$SRC_DIR" fetch --tags --prune origin
    run git -C "$SRC_DIR" reset --hard "origin/${REPO_REF}" 2>/dev/null \
      || run git -C "$SRC_DIR" checkout "${REPO_REF}"
  else
    say "Clonando repo (esta é a primeira vez)…"
    run git clone --depth 1 --branch "${REPO_REF}" "${REPO_URL}" "${SRC_DIR}" 2>/dev/null \
      || run git clone "${REPO_URL}" "${SRC_DIR}"
    run git -C "${SRC_DIR}" checkout "${REPO_REF}" 2>/dev/null || true
  fi
  COMMIT_SHA=$(git -C "${SRC_DIR}" rev-parse HEAD 2>/dev/null || echo unknown)
  COMMIT_MSG=$(git -C "${SRC_DIR}" log -1 --oneline 2>/dev/null || echo unknown)
  say "Commit em uso: ${COMMIT_SHA:0:12}  '${COMMIT_MSG}'"

  # ---------- 1d. build ---------------------------------------------------
  BUILD_OUT="${SRC_DIR}/bin/nsx-collector"
  say "Build do binário em ${BUILD_OUT} (linux/amd64, CGO=0, stripped)…"
  run mkdir -p "${SRC_DIR}/bin"
  if [[ $DRY_RUN -eq 1 ]]; then
    log "DRY-RUN: pularia o go build aqui (resultado simulado em ${BUILD_OUT})"
  else
    # Build isolado em SRC_DIR — NÃO toca em /home/nsx_collector.
    if ! ( cd "${SRC_DIR}" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
             go build -trimpath -ldflags="-s -w" -o "${BUILD_OUT}" ./cmd/ ); then
      fatal "go build FALHOU. Nada foi alterado em ${INSTALL_DIR}. Veja erros acima." 7
    fi
  fi
  # Confirma binário gerado.
  if [[ $DRY_RUN -eq 0 ]]; then
    [[ -f "${BUILD_OUT}" ]] || fatal "build não produziu ${BUILD_OUT}" 7
    BUILD_SHA=$(sha256sum "${BUILD_OUT}" | awk '{print $1}')
    BUILD_SIZE=$(du -h "${BUILD_OUT}" | awk '{print $1}')
    say "Build OK — ${BUILD_OUT} (${BUILD_SIZE}, sha256 ${BUILD_SHA:0:16}…)"
  fi
  BIN_SOURCE="${BUILD_OUT}"
fi

# ---------- 2. preflight ---------------------------------------------------
say "Pré-checks…"

command -v systemctl >/dev/null 2>&1 || fatal "systemctl não encontrado — esta máquina usa outro init?" 2
command -v curl       >/dev/null 2>&1 || fatal "curl não encontrado — instale antes (yum/dnf install curl, apt install curl)." 2

# Detecta distro só pra log (não bloqueia: backup/start/stop são systemd puros)
DISTRO=$( [ -r /etc/os-release ] && . /etc/os-release && echo "$ID-$VERSION_ID" || echo "unknown")
say "Distro: $DISTRO"

# Binário fonte
[[ -f "$BIN_SOURCE" ]] || fatal "BIN_SOURCE não existe: $BIN_SOURCE  (envie via scp e re-rode)" 2
file "$BIN_SOURCE" 2>/dev/null | grep -qE 'ELF .* (executable|shared object)' \
  || fatal "BIN_SOURCE não parece um binário Linux ELF: $BIN_SOURCE" 2

# Service exists?
if ! systemctl list-unit-files "${SERVICE}.service" 2>/dev/null | grep -q "$SERVICE"; then
  say "service '${SERVICE}' ainda não está instalado — vou usar o unit file do repo (se presente)"
fi

# Install dir
[[ -d "$INSTALL_DIR" ]] || fatal "diretório de instalação não existe: $INSTALL_DIR" 2
[[ -d "${INSTALL_DIR}/configs" ]] || fatal "${INSTALL_DIR}/configs ausente — instalação inicial deve usar setup.sh" 2

# Espaço em disco para backup (precisa ~50MB folga)
AVAIL_KB=$(df -kP "$INSTALL_DIR" | awk 'NR==2 {print $4}')
[[ $AVAIL_KB -lt 51200 ]] && fatal "menos de 50MB livres em $INSTALL_DIR — libere espaço antes" 2
say "Espaço livre OK (${AVAIL_KB} KB em $(df -P "$INSTALL_DIR" | awk 'NR==2 {print $6}'))"

# influxdb config — precisa do bucket capacity_bucket setado p/ a coleta nova
if ! grep -qE '^\s*capacity_bucket\s*:' "${INSTALL_DIR}/configs/config.yaml"; then
  fatal "config.yaml não tem 'capacity_bucket:' — adicione antes (a coleta nova grava lá)" 2
fi

# ---------- 3. backup ------------------------------------------------------
say "Backup completo em: $BACKUP_DIR"
run mkdir -p "$BACKUP_DIR"

if [[ -f "${INSTALL_DIR}/nsx-collector" ]]; then
  run cp -p "${INSTALL_DIR}/nsx-collector" "$BACKUP_DIR/"
  CURRENT_SUM=$(sha256sum "${INSTALL_DIR}/nsx-collector" | awk '{print $1}')
  log "binário atual sha256: $CURRENT_SUM"
else
  log "nenhum binário atual em ${INSTALL_DIR} (instalação nova?)"
fi
NEW_SUM=$(sha256sum "$BIN_SOURCE" | awk '{print $1}')
log "binário novo sha256:   $NEW_SUM"
if [[ "${CURRENT_SUM:-}" == "$NEW_SUM" ]]; then
  say "Binário novo == binário atual. Nada a fazer."
  trap - EXIT; exit 0
fi

# configs (inteiro, vem com config.yaml + managers.yaml)
run cp -rp "${INSTALL_DIR}/configs" "$BACKUP_DIR/configs"
# .env (segredos — backup com mesmo perms)
[[ -f "${INSTALL_DIR}/.env" ]] && run cp -p "${INSTALL_DIR}/.env" "$BACKUP_DIR/.env"
# unit file
if [[ -f /etc/systemd/system/nsx-collector.service ]]; then
  run cp -p /etc/systemd/system/nsx-collector.service "$BACKUP_DIR/"
fi
# state (HA + t1watch) — não enorme, tem valor pro rollback
if [[ -d "${INSTALL_DIR}/state" ]]; then
  run cp -rp "${INSTALL_DIR}/state" "$BACKUP_DIR/state"
fi

# Metadata humana
{
  echo "deploy_capacitynovo backup"
  echo "host=$(hostname -f 2>/dev/null || hostname)"
  echo "timestamp_utc=$TS"
  echo "binario_atual_sha256=${CURRENT_SUM:-none}"
  echo "binario_novo_sha256=$NEW_SUM"
  echo "service=$SERVICE"
  echo "install_dir=$INSTALL_DIR"
  echo "distro=$DISTRO"
  echo "repo_url=${REPO_URL}"
  echo "repo_ref=${REPO_REF}"
  echo "commit_sha=${COMMIT_SHA:-skipped}"
  echo "commit_msg=${COMMIT_MSG:-skipped}"
  systemctl is-active "$SERVICE" 2>/dev/null | sed 's/^/service_state_pre=/' || true
} > "${BACKUP_DIR}/MANIFEST.txt"
say "Backup pronto."

# ---------- 4. patch idempotente do config.yaml ---------------------------
say "Patch idempotente em config.yaml (blocos t1_watch / capacity)…"
CFG="${INSTALL_DIR}/configs/config.yaml"

if ! grep -qE '^\s*t1_watch\s*:' "$CFG"; then
  if [[ $DRY_RUN -eq 1 ]]; then
    log "DRY-RUN: appendaria bloco t1_watch em $CFG"
  else
    cat >> "$CFG" <<'YAML'

# === Adicionado por deploy_capacitynovo.sh ===
# Detector de criação de T1 -> Slack bot.
# Comece com enabled: false para baseline; depois ligue.
t1_watch:
  enabled: false
  slack_channel: ""                       # vazio = usa slack.channel
  state_dir: "/home/nsx_collector/state"
  vrf_t1_limit_default: 200
  t0_t1_limit_default: 1000
  vrf_t1_limits: {}
  t0_t1_limits: {}
YAML
    log "bloco t1_watch acrescentado a $CFG"
  fi
else
  log "bloco t1_watch já existe em $CFG — preservando"
fi

if ! grep -qE '^\s*capacity\s*:' "$CFG"; then
  if [[ $DRY_RUN -eq 1 ]]; then
    log "DRY-RUN: appendaria bloco capacity em $CFG"
  else
    cat >> "$CFG" <<'YAML'

# Cobertura estendida do painel Capacity NSX (segments, FW por gateway, groups).
# NAT por T1 é caro (~1 req por T1): ligue só depois de validar carga.
capacity:
  collect_segments: true
  collect_gw_policies: true
  collect_groups: true
  collect_nat_per_t1: false
  nat_per_t1_pace_ms: 30
  nat_per_t1_parallel: 4
YAML
    log "bloco capacity acrescentado a $CFG"
  fi
else
  log "bloco capacity já existe em $CFG — preservando"
fi

# Diretório de state precisa existir e ser do usuário do serviço.
run mkdir -p "${INSTALL_DIR}/state"
run chown -R "${SERVICE_USER}:${SERVICE_USER}" "${INSTALL_DIR}/state" 2>/dev/null || true

# ---------- 5. swap do binário --------------------------------------------
say "Stop, swap atômico, start."

PRE_STATE=$(systemctl is-active "$SERVICE" 2>/dev/null || echo "inactive")
log "estado pré-deploy do service: $PRE_STATE"

if [[ "$PRE_STATE" == "active" ]]; then
  run systemctl stop "$SERVICE"
fi

# Copia para tmp ao lado do destino (mesmo filesystem → mv atômico).
TMP_BIN="${INSTALL_DIR}/nsx-collector.new.$$"
run cp -p "$BIN_SOURCE" "$TMP_BIN"
run chmod 0755 "$TMP_BIN"
run chown "${SERVICE_USER}:${SERVICE_USER}" "$TMP_BIN" 2>/dev/null || true
run mv -f "$TMP_BIN" "${INSTALL_DIR}/nsx-collector"

# Garante unit file presente (caso seja primeiro deploy via este script).
SRC_UNIT="$(cd "$(dirname "$0")" && pwd)/nsx-collector.service"
if [[ -f "$SRC_UNIT" && ! -f /etc/systemd/system/nsx-collector.service ]]; then
  run cp -p "$SRC_UNIT" /etc/systemd/system/nsx-collector.service
  run systemctl daemon-reload
fi

run systemctl start "$SERVICE"
run systemctl enable "$SERVICE"   # memória: sem isto não volta após reboot.

# ---------- 6. health check -----------------------------------------------
do_rollback() {
  log "ROLLBACK automático: restaurando a partir de $BACKUP_DIR"
  run systemctl stop "$SERVICE" || true
  if [[ -f "$BACKUP_DIR/nsx-collector" ]]; then
    run cp -p "$BACKUP_DIR/nsx-collector" "${INSTALL_DIR}/nsx-collector"
  fi
  if [[ -d "$BACKUP_DIR/configs" ]]; then
    run rm -rf "${INSTALL_DIR}/configs"
    run cp -rp "$BACKUP_DIR/configs" "${INSTALL_DIR}/configs"
  fi
  run systemctl start "$SERVICE" || true
  log "Rollback concluído. Verifique journalctl -u $SERVICE -n 100"
  fatal "Deploy revertido por falha de health check." 5
}

if [[ $SKIP_HEALTH -eq 1 ]]; then
  say "SKIP_HEALTH=1 — pulei a verificação (NÃO recomendado em prod)."
else
  say "Health check (timeout ${HEALTH_TIMEOUT}s): /metrics responde + ciclos crescem"
  END=$(( $(date +%s) + HEALTH_TIMEOUT ))
  CYCLES_INIT=""
  HEALTH_OK=0
  while [[ $(date +%s) -lt $END ]]; do
    if ! systemctl is-active --quiet "$SERVICE"; then
      log "service $SERVICE não está active — abortando"
      break
    fi
    # /metrics responde?
    if BODY=$(curl -fsS --max-time 5 "$METRICS_URL" 2>/dev/null); then
      # Pegamos a soma das collect_cycles_total. Se está subindo, o worker
      # roda. Se fica parado mas /metrics responde, ainda está inicializando.
      CYCLES=$(printf '%s\n' "$BODY" \
        | awk '/^nsx_collector_collect_cycles_total\{/ {gsub(/[^0-9.]/,"",$NF); sum+=$NF} END {print sum+0}')
      if [[ -z "$CYCLES_INIT" ]]; then
        CYCLES_INIT="$CYCLES"
        log "inicial collect_cycles_total = $CYCLES_INIT — aguardando próximo ciclo"
      elif awk -v a="$CYCLES" -v b="$CYCLES_INIT" 'BEGIN{exit !(a>b)}'; then
        log "ciclos progrediram: ${CYCLES_INIT} → ${CYCLES} — saudável"
        HEALTH_OK=1
        break
      fi
    else
      log "/metrics ainda não respondeu — esperando…"
    fi
    sleep 5
  done

  if [[ $HEALTH_OK -ne 1 ]]; then
    log "Health check falhou (sem progresso de ciclos em ${HEALTH_TIMEOUT}s)."
    journalctl -u "$SERVICE" -n 40 --no-pager 2>/dev/null | tee -a "$LOG_FILE" >&2 || true
    do_rollback
  fi
fi

say "Service saudável. Estado: $(systemctl is-active "$SERVICE")"

# ---------- 7. prune de backups antigos -----------------------------------
say "Prune de backups antigos (mantém os ${KEEP_BACKUPS} mais recentes)"
if [[ -d "$BACKUP_ROOT" ]]; then
  # shellcheck disable=SC2012
  ls -1t "$BACKUP_ROOT" 2>/dev/null | tail -n +"$((KEEP_BACKUPS+1))" | while read -r old; do
    [[ -z "$old" ]] && continue
    log "removendo backup antigo: $old"
    run rm -rf "${BACKUP_ROOT:?}/${old}"
  done
fi

# ---------- 8. resumo final -----------------------------------------------
trap - EXIT
say "DEPLOY OK."
say "Resumo:"
say "  service:        $(systemctl is-active "$SERVICE")"
say "  binário:        ${INSTALL_DIR}/nsx-collector  (sha256 ${NEW_SUM:0:12}…)"
if [[ $SKIP_BUILD -ne 1 ]]; then
  say "  fonte:          ${SRC_DIR}  (commit ${COMMIT_SHA:0:12} '${COMMIT_MSG:-}')"
fi
say "  backup deste:   ${BACKUP_DIR}"
say "  log:            ${LOG_FILE}"
say "  rollback:       sudo ROLLBACK_TO=${BACKUP_DIR} $0 --rollback"
say "Para ligar o bot Slack edite t1_watch.enabled: true em ${INSTALL_DIR}/configs/config.yaml e:"
say "  sudo systemctl restart $SERVICE   # primeiro ciclo só baseline, sem flood."
