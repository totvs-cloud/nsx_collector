#!/usr/bin/env bash
# Upgrade cirurgico do nsx-collector pra ganhar coleta HA, em VMs que NAO
# tem /home/nsx_collector/.git (instalacao via deploy-tece.sh / deploy-tesp0X.sh
# original). Faz:
#   - Backup completo com timestamp
#   - git clone --depth 1 em /tmp + build
#   - Patches IDEMPOTENTES em configs/config.yaml e configs/managers.yaml
#     (intervals.ha + state_dir + ha_watch)
#   - Stop / swap do binario / Start
#   - Health check com ROLLBACK AUTOMATICO se o service nao subir
#   - Smoke test HA: aguarda 90s e procura logs do 1o ciclo
#
# Nao toca em .env (preserva credenciais NSX/Influx atuais).
# Idempotente: rodar de novo nao causa efeito colateral.
#
# Uso:
#   sudo bash upgrade-ha-existing.sh
#
# Variaveis (opcionais):
#   SITE      Override do nome do site (default: detectado do managers.yaml)
#   REPO_URL  Override do repo (default: git@github.com:totvs-cloud/nsx_collector.git)
#   INSTALL   Override do install dir (default: /home/nsx_collector)

set -uo pipefail

INSTALL="${INSTALL:-/home/nsx_collector}"
REPO_URL="${REPO_URL:-git@github.com:totvs-cloud/nsx_collector.git}"
TS=$(date +%Y%m%d_%H%M%S)
BAK="$INSTALL/_bak_$TS"
SRC="/tmp/nsx-collector-src-$TS"

step()  { echo -e "\n\033[1;36m==> $*\033[0m"; }
ok()    { echo -e "    \033[0;32m[OK]\033[0m $*"; }
warn()  { echo -e "    \033[0;33m[WARN]\033[0m $*" >&2; }
fail()  { echo -e "    \033[0;31m[ERRO]\033[0m $*" >&2; }

# --- pre-checks ---------------------------------------------------------------
if [ "$(id -u)" -ne 0 ]; then
  fail "rode como root (sudo)"; exit 1
fi

if [ ! -d "$INSTALL" ] || [ ! -f "$INSTALL/nsx-collector" ] || [ ! -d "$INSTALL/configs" ]; then
  fail "$INSTALL nao parece ter uma instalacao do nsx-collector"; exit 1
fi

# Detecta nome do site se nao passado
if [ -z "${SITE:-}" ]; then
  SITE=$(awk -F'"' '/^[[:space:]]*-?[[:space:]]*site:/{print $2; exit}' "$INSTALL/configs/managers.yaml" || true)
fi
if [ -z "${SITE:-}" ]; then
  warn "nao consegui detectar SITE do managers.yaml — smoke test ficara parcial"
fi

# --- 1. Go disponivel ---------------------------------------------------------
step "Verificando Go"
export PATH="$PATH:/usr/local/go/bin"
if ! command -v go &>/dev/null; then
  fail "go nao encontrado em PATH nem /usr/local/go/bin"
  fail "instale com:"
  fail "  curl -sSL https://go.dev/dl/go1.23.6.linux-amd64.tar.gz | tar -C /usr/local -xz"
  exit 1
fi
ok "$(go version)"

# --- 2. Backup ---------------------------------------------------------------
step "Backup atual em $BAK"
mkdir -p "$BAK"
cp -a "$INSTALL/nsx-collector"                   "$BAK/nsx-collector"
cp -a "$INSTALL/configs"                         "$BAK/configs"
[ -f "$INSTALL/.env" ] && cp -a "$INSTALL/.env"  "$BAK/.env"
cp -a /etc/systemd/system/nsx-collector.service  "$BAK/nsx-collector.service" 2>/dev/null || true
ok "backup completo (binario + configs + .env + service unit)"
ROLLBACK_CMD="systemctl stop nsx-collector && cp -a $BAK/nsx-collector $INSTALL/ && cp -a $BAK/configs/. $INSTALL/configs/ && systemctl start nsx-collector"
echo "    rollback manual a qualquer momento:"
echo "      $ROLLBACK_CMD"

# --- 3. Clone + build --------------------------------------------------------
step "Clonando master em $SRC"
rm -rf "$SRC"
if ! git clone --depth 1 -b master "$REPO_URL" "$SRC"; then
  fail "git clone falhou — sem alteracoes na VM. Abortando."
  exit 1
fi
ok "clonado"

step "Compilando"
if ! ( cd "$SRC" && CGO_ENABLED=0 go build -o /tmp/nsx-collector-new ./cmd/ ); then
  fail "build falhou — sem alteracoes na VM. Abortando."
  exit 1
fi
NEW_HASH=$(sha256sum /tmp/nsx-collector-new | awk '{print $1}')
OLD_HASH=$(sha256sum "$INSTALL/nsx-collector" | awk '{print $1}')
ok "build OK"
echo "    binario atual:  $OLD_HASH"
echo "    binario novo:   $NEW_HASH"
if [ "$OLD_HASH" = "$NEW_HASH" ]; then
  warn "binarios identicos — nada a atualizar (mas patches em configs podem rodar)"
fi

# --- 4. Patches idempotentes nos configs --------------------------------------
step "Patch idempotente em configs/config.yaml (intervals.slow + intervals.ha)"
CFG="$INSTALL/configs/config.yaml"
ADDED_CFG=0
if ! grep -qE '^[[:space:]]+slow:[[:space:]]' "$CFG"; then
  sed -i '/^[[:space:]]\+traffic:/a\  slow: 5m' "$CFG"
  ADDED_CFG=1
fi
if ! grep -qE '^[[:space:]]+ha:[[:space:]]' "$CFG"; then
  # insere logo apos slow se acabamos de adicionar, senao apos traffic
  if grep -qE '^[[:space:]]+slow:[[:space:]]' "$CFG"; then
    sed -i '/^[[:space:]]\+slow:[[:space:]]/a\  ha: 1m' "$CFG"
  else
    sed -i '/^[[:space:]]\+traffic:/a\  ha: 1m' "$CFG"
  fi
  ADDED_CFG=1
fi
if [ "$ADDED_CFG" -eq 1 ]; then
  ok "config.yaml atualizado:"
  grep -E '^[[:space:]]+(default|traffic|slow|ha):' "$CFG" | sed 's/^/      /'
else
  ok "config.yaml ja tinha intervals.slow + intervals.ha"
fi

step "Patch idempotente em configs/managers.yaml (state_dir + ha_watch)"
MGR="$INSTALL/configs/managers.yaml"
if grep -qE '^[[:space:]]+ha_watch:[[:space:]]*$' "$MGR"; then
  ok "ha_watch ja existe — nada a fazer"
else
  cat >> "$MGR" <<'EOF'
    state_dir: /home/nsx_collector/state
    ha_watch:
      mode: auto
      size: 10
      t1_names: []
EOF
  ok "state_dir + ha_watch acrescentados ao(s) manager(s)"
  echo "    >>> ATENCAO: se houver MAIS de 1 manager no arquivo, o append foi feito"
  echo "    >>> apenas no fim. Se for o caso, ajuste manualmente (cada manager"
  echo "    >>> precisa ter seu proprio bloco state_dir/ha_watch)."
fi

step "Criando state_dir"
mkdir -p "$INSTALL/state"
chown root:root "$INSTALL/state"
chmod 755 "$INSTALL/state"
ok "$INSTALL/state pronto"

# --- 5. Swap do binario com janela minima ------------------------------------
step "Stop + swap binario"
systemctl stop nsx-collector
mv -f /tmp/nsx-collector-new "$INSTALL/nsx-collector"
chmod +x "$INSTALL/nsx-collector"
ok "binario novo instalado"

step "Start"
systemctl start nsx-collector
sleep 5

# --- 6. Health check com rollback automatico ---------------------------------
if ! systemctl is-active --quiet nsx-collector; then
  fail "service NAO subiu — executando rollback automatico"
  systemctl stop nsx-collector || true
  cp -a "$BAK/nsx-collector" "$INSTALL/nsx-collector"
  cp -a "$BAK/configs/." "$INSTALL/configs/"
  systemctl start nsx-collector
  fail "rollback aplicado. Logs do erro que provocou a falha:"
  journalctl -u nsx-collector -n 30 --no-pager
  exit 1
fi
ok "service ativo (PID $(systemctl show -p MainPID --value nsx-collector))"

step "Ultimas 10 linhas do journal"
journalctl -u nsx-collector -n 10 --no-pager

# --- 7. Smoke HA (apos 90s deve ter rodado 1 ciclo) --------------------------
step "Aguardando 90s pro 1o ciclo HA..."
sleep 90
HA_LOGS=$(journalctl -u nsx-collector --since "2 minutes ago" --no-pager 2>/dev/null \
          | grep -iE '"logger":"[^"]*\.ha"|ha collected|"ha"' | tail -5 || true)
if [ -n "$HA_LOGS" ]; then
  ok "coleta HA detectada nos logs:"
  echo "$HA_LOGS" | sed 's/^/      /'
else
  warn "nenhuma evidencia de coleta HA em 90s"
  warn "verifique manualmente: journalctl -u nsx-collector -f | grep -iE 'ha:|failover'"
fi

step "State file gerado?"
ls -la "$INSTALL/state/" 2>/dev/null || true
if [ -n "${SITE:-}" ]; then
  STATE_FILE="$INSTALL/state/ha-watch-$(echo "$SITE" | tr '[:upper:]' '[:lower:]').json"
  if [ -f "$STATE_FILE" ]; then
    ok "inventario criado: $STATE_FILE"
    head -40 "$STATE_FILE" | sed 's/^/      /'
  else
    warn "$STATE_FILE ainda nao apareceu — pode levar 1-2 ciclos extras"
  fi
fi

# --- 8. Limpeza --------------------------------------------------------------
rm -rf "$SRC"

cat <<EOF

$(echo -e "\033[1;32m==> Upgrade concluido com sucesso.\033[0m")
    Backup completo em: $BAK
    Rollback (a qualquer momento):
      $ROLLBACK_CMD

    Proximos passos:
      - Acompanhar coleta: journalctl -u nsx-collector -f | grep -iE 'ha:|failover'
      - Inventario:        cat $INSTALL/state/ha-watch-*.json | jq .
      - Influx (3 measurements novos): nsx_ha_state, nsx_ha_cluster_summary, nsx_ha_change
      - Dashboard tatico: importar dashboards/nsx-ha-tatico.json no Grafana
EOF
