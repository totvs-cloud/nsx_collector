#!/usr/bin/env bash
# Preflight check pro nsx-collector (READ-ONLY).
#
# Roda TUDO que verificamos manualmente no TECE antes do 1o upgrade,
# automatizado. Nao modifica nada. Saida em "PASS / WARN / FAIL" + veredito
# final dizendo se da pra rodar o upgrade-ha-existing.sh ou nao.
#
# Uso:
#   sudo bash preflight-check.sh
#
# Detecta tudo do proprio /home/nsx_collector (managers.yaml + .env),
# entao serve sem mudancas pra TESP02-07, TECE, etc.

set -uo pipefail
INSTALL=/home/nsx_collector
PASS=0; WARN=0; FAIL=0

ok()      { echo -e "  \033[0;32m[PASS]\033[0m $*"; PASS=$((PASS+1)); }
warn()    { echo -e "  \033[0;33m[WARN]\033[0m $*"; WARN=$((WARN+1)); }
fail()    { echo -e "  \033[0;31m[FAIL]\033[0m $*"; FAIL=$((FAIL+1)); }
info()    { echo -e "  \033[0;36m[INFO]\033[0m $*"; }
section() { echo -e "\n\033[1;36m=== $* ===\033[0m"; }

if [ "$(id -u)" -ne 0 ]; then
  echo "rode como root (sudo)"; exit 2
fi

# ============================================================================
section "1. Estrutura da instalacao em $INSTALL"
# ============================================================================
[ -d "$INSTALL" ]                && ok "diretorio $INSTALL existe"     || { fail "$INSTALL nao existe — collector nao instalado?"; exit 1; }
[ -f "$INSTALL/nsx-collector" ]  && ok "binario presente"              || fail "binario ausente"
[ -d "$INSTALL/configs" ]        && ok "configs/ presente"             || fail "configs/ ausente"
[ -f "$INSTALL/.env" ]           && ok ".env presente"                 || fail ".env ausente"

if [ -d "$INSTALL/.git" ]; then
  info ".git EXISTE -> instalado via update-collectors.sh; PODE usar update-collectors.sh OU upgrade-ha-existing.sh"
else
  info ".git AUSENTE -> instalado via deploy-*.sh; USAR upgrade-ha-existing.sh (update-collectors.sh vira no-op)"
fi

if [ -f "$INSTALL/nsx-collector" ]; then
  HASH=$(sha256sum "$INSTALL/nsx-collector" | awk '{print $1}')
  SIZE=$(stat -c %s "$INSTALL/nsx-collector")
  MTIME=$(stat -c %y "$INSTALL/nsx-collector" | cut -d. -f1)
  info "binario sha256: $HASH"
  info "binario size:   $SIZE bytes   mtime: $MTIME"
fi

# ============================================================================
section "2. configs/config.yaml — intervals"
# ============================================================================
CFG="$INSTALL/configs/config.yaml"
if [ -f "$CFG" ]; then
  ok "config.yaml legivel"
  if grep -qE '^[[:space:]]+ha:[[:space:]]' "$CFG"; then
    ok "intervals.ha JA configurado"
  else
    warn "intervals.ha AUSENTE (upgrade vai adicionar 'ha: 1m')"
  fi
  if grep -qE '^[[:space:]]+slow:[[:space:]]' "$CFG"; then
    ok "intervals.slow JA configurado"
  else
    warn "intervals.slow AUSENTE (upgrade vai adicionar 'slow: 5m')"
  fi
  echo "  intervals atuais:"
  grep -A5 '^intervals:' "$CFG" | sed 's/^/    /'
else
  fail "config.yaml ausente"
fi

# ============================================================================
section "3. configs/managers.yaml — managers e ha_watch"
# ============================================================================
MGR="$INSTALL/configs/managers.yaml"
if [ -f "$MGR" ]; then
  ok "managers.yaml legivel"
  N_MGR=$(grep -cE '^[[:space:]]*-[[:space:]]+site:' "$MGR")
  info "managers configurados: $N_MGR"
  grep -E '^[[:space:]]*-[[:space:]]+site:|^[[:space:]]+url:|^[[:space:]]+enabled:' "$MGR" | sed 's/^/    /'

  if grep -qE '^[[:space:]]+ha_watch:[[:space:]]*$' "$MGR"; then
    ok "ha_watch JA configurado"
  else
    warn "ha_watch AUSENTE (upgrade vai adicionar bloco padrao)"
  fi
  if grep -qE '^[[:space:]]+state_dir:[[:space:]]' "$MGR"; then
    ok "state_dir JA configurado"
  else
    warn "state_dir AUSENTE (upgrade vai adicionar)"
  fi

  if [ "$N_MGR" -gt 1 ] && ! grep -qE '^[[:space:]]+ha_watch:[[:space:]]*$' "$MGR"; then
    warn "ATENCAO: $N_MGR managers no arquivo. upgrade-ha-existing.sh"
    warn "         appenda ha_watch apenas no FIM. Ajuste manualmente depois,"
    warn "         duplicando o bloco (cada manager precisa do seu)."
  fi
else
  fail "managers.yaml ausente"
fi

# ============================================================================
section "4. systemd / journal"
# ============================================================================
if systemctl is-active --quiet nsx-collector; then
  ok "service ATIVO"
  PID=$(systemctl show -p MainPID --value nsx-collector)
  STARTED=$(systemctl show -p ActiveEnterTimestamp --value nsx-collector)
  info "PID: $PID   started: $STARTED"
else
  fail "service INATIVO (ou nao instalado)"
fi

ERR_COUNT=$(journalctl -u nsx-collector --since "1 hour ago" 2>/dev/null \
            | grep -ciE 'error|fail|panic' || true)
if   [ "$ERR_COUNT" -gt 20 ]; then fail "Muitos erros na ultima hora: $ERR_COUNT"
elif [ "$ERR_COUNT" -gt  0 ]; then warn "Erros na ultima hora: $ERR_COUNT"
else                               ok   "Nenhum erro na ultima hora"
fi

echo "  ultimas 5 linhas:"
journalctl -u nsx-collector -n 5 --no-pager 2>/dev/null | sed 's/^/    /'

# ============================================================================
section "5. InfluxDB — token + auth"
# ============================================================================
# Extrai com cut -d= -f2- (preserva == final do token InfluxDB)
INFLUX_TOKEN=$(grep -E '^INFLUX_TOKEN=' "$INSTALL/.env" 2>/dev/null \
               | cut -d= -f2- | tr -d '"' | tr -d "'")
INFLUX_URL=$(grep -E '^[[:space:]]+url:' "$CFG" 2>/dev/null | head -1 | awk -F'"' '{print $2}')
INFLUX_ORG=$(grep -E '^[[:space:]]+org:' "$CFG" 2>/dev/null | head -1 | awk -F'"' '{print $2}')
INFLUX_BUCKET=$(grep -E '^[[:space:]]+bucket:' "$CFG" 2>/dev/null | head -1 | awk -F'"' '{print $2}')
INFLUX_URL="${INFLUX_URL:-http://10.114.35.75:8086}"
INFLUX_ORG="${INFLUX_ORG:-TOTVS}"
INFLUX_BUCKET="${INFLUX_BUCKET:-nsx}"

info "URL=$INFLUX_URL  org=$INFLUX_ORG  bucket=$INFLUX_BUCKET"

INFLUX_OK=0
if [ -z "$INFLUX_TOKEN" ]; then
  fail "INFLUX_TOKEN ausente no .env"
else
  TOKEN_LEN=${#INFLUX_TOKEN}
  if [ "$TOKEN_LEN" -lt 60 ]; then
    fail "token suspeito ($TOKEN_LEN chars; esperado ~88) — extracao quebrada?"
  else
    ok "token extraido ($TOKEN_LEN chars)"
  fi
  HTTP=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 \
         --request POST "$INFLUX_URL/api/v2/query?org=$INFLUX_ORG" \
         --header "Authorization: Token $INFLUX_TOKEN" \
         --header "Content-Type: application/vnd.flux" \
         --header "Accept: text/csv" \
         --data-raw "from(bucket:\"$INFLUX_BUCKET\") |> range(start:-1m) |> limit(n:1)" \
         2>/dev/null)
  if   [ "$HTTP" = "200" ]; then ok "Influx HTTP 200 (token valido + bucket existe)"; INFLUX_OK=1
  elif [ "$HTTP" = "401" ]; then fail "Influx HTTP 401 — token nao autorizado"
  elif [ "$HTTP" = "404" ]; then fail "Influx HTTP 404 — bucket '$INFLUX_BUCKET' nao existe"
  elif [ "$HTTP" = "000" ]; then fail "Influx INALCANCAVEL ($INFLUX_URL) — network/TLS/timeout"
  else                            fail "Influx HTTP $HTTP"
  fi
fi

# ============================================================================
section "6. NSX Manager — alcance + credenciais + inventario"
# ============================================================================
NSX_URL=$(awk -F'"' '/^[[:space:]]+url:/{print $2; exit}' "$MGR" 2>/dev/null)
NSX_USER_ENV=$(awk -F'"' '/^[[:space:]]+user_env:/{print $2; exit}' "$MGR" 2>/dev/null)
NSX_PASS_ENV=$(awk -F'"' '/^[[:space:]]+password_env:/{print $2; exit}' "$MGR" 2>/dev/null)
SITE_NAME=$(awk -F'"' '/^[[:space:]]*-[[:space:]]+site:/{print $2; exit}' "$MGR" 2>/dev/null)
NSX_USER=$(grep -E "^${NSX_USER_ENV}=" "$INSTALL/.env" 2>/dev/null | cut -d= -f2- | tr -d '"' | tr -d "'")
NSX_PASS=$(grep -E "^${NSX_PASS_ENV}=" "$INSTALL/.env" 2>/dev/null | cut -d= -f2- | tr -d '"' | tr -d "'")

info "site:        $SITE_NAME"
info "NSX URL:     $NSX_URL"
info "user_env=$NSX_USER_ENV  pass_env=$NSX_PASS_ENV"
info "user lido:   ${NSX_USER:-AUSENTE}"
info "pass lido:   ${NSX_PASS:+(*** $(echo -n "$NSX_PASS" | wc -c) chars ***)}"

if [ -z "$NSX_URL" ] || [ -z "$NSX_USER" ] || [ -z "$NSX_PASS" ]; then
  fail "credenciais NSX incompletas"
else
  HTTP=$(curl -sk -o /dev/null -w "%{http_code}" --max-time 10 \
         -u "$NSX_USER:$NSX_PASS" "$NSX_URL/api/v1/cluster/status" 2>/dev/null)
  case "$HTTP" in
    200)  ok "NSX HTTP 200 (credenciais validas)"
          N_T0=$(curl -sk --max-time 10 -u "$NSX_USER:$NSX_PASS" \
                 "$NSX_URL/api/v1/logical-routers?router_type=TIER0&page_size=50" 2>/dev/null \
                 | python3 -c "import sys,json;d=json.load(sys.stdin);print(len(d.get('results',[])))" 2>/dev/null \
                 || echo "?")
          N_T1=$(curl -sk --max-time 15 -u "$NSX_USER:$NSX_PASS" \
                 "$NSX_URL/api/v1/logical-routers?router_type=TIER1&page_size=400" 2>/dev/null \
                 | python3 -c "import sys,json;d=json.load(sys.stdin);print(len(d.get('results',[])))" 2>/dev/null \
                 || echo "?")
          info "T0s descobertos: $N_T0   T1s descobertos: $N_T1"
          if [ "$N_T0" != "?" ] && [ "$N_T0" -gt 0 ]; then
            # cardinalidade esperada apos coleta: $N_T0 edge clusters x 10 T1s = $((N_T0*10))
            info "apos upgrade: ~$((N_T0)) edge clusters x 10 T1s observados = ~$((N_T0*10)) T1s em nsx_ha_state"
          fi ;;
    401|403) fail "NSX HTTP $HTTP — credenciais rejeitadas" ;;
    000)     fail "NSX INALCANCAVEL (HTTP 000) — network/TLS/timeout" ;;
    *)       fail "NSX HTTP $HTTP" ;;
  esac
fi

# ============================================================================
section "7. Estado HA atual no Influx (se ja coleta)"
# ============================================================================
if [ "$INFLUX_OK" = "1" ] && [ -n "$SITE_NAME" ]; then
  R=$(curl -s --max-time 10 --request POST \
      "$INFLUX_URL/api/v2/query?org=$INFLUX_ORG" \
      --header "Authorization: Token $INFLUX_TOKEN" \
      --header "Content-Type: application/vnd.flux" \
      --header "Accept: text/csv" \
      --data-raw "from(bucket:\"$INFLUX_BUCKET\") |> range(start:-5m) |> filter(fn:(r) => r._measurement == \"nsx_ha_cluster_summary\" and r.site == \"$SITE_NAME\" and r._field == \"observed\") |> last() |> keep(columns:[\"t0_name\"])" 2>/dev/null)
  HA_CLUSTERS=$(echo "$R" | awk -F, 'NR>1 && $NF!="" {print $NF}' | sort -u | wc -l)
  if [ "$HA_CLUSTERS" -gt 0 ]; then
    info "Influx ja reporta $HA_CLUSTERS edge cluster(s) HA pro site $SITE_NAME (clusters: $(echo "$R" | awk -F, 'NR>1 && $NF!="" {print $NF}' | sort -u | tr '\n' ' ' | tr -d '"'))"
    info "  -> upgrade vai continuar com a mesma estrutura, sem reset"
  else
    info "Influx NAO tem dados HA pra $SITE_NAME ainda (esperado; upgrade vai inicializar)"
  fi
fi

# ============================================================================
section "8. State dir"
# ============================================================================
if [ -d "$INSTALL/state" ]; then
  info "$INSTALL/state JA existe — conteudo:"
  ls -la "$INSTALL/state/" 2>/dev/null | head -10 | sed 's/^/    /'
else
  info "$INSTALL/state nao existe ainda (upgrade vai criar)"
fi

# ============================================================================
section "Resumo"
# ============================================================================
echo "  PASS: $PASS   WARN: $WARN   FAIL: $FAIL"
echo
if   [ "$FAIL" -gt 0 ]; then
  echo -e "  \033[0;31m=> NAO prossiga com upgrade-ha-existing.sh enquanto houver FAILs.\033[0m"
  exit 1
elif [ "$WARN" -gt 0 ]; then
  echo -e "  \033[0;33m=> WARNs sao esperados quando ainda nao tem HA configurado.\033[0m"
  echo -e "  \033[0;33m   Pode prosseguir com:  sudo bash upgrade-ha-existing.sh\033[0m"
  exit 0
else
  echo -e "  \033[0;32m=> Tudo OK. Pode rodar:  sudo bash upgrade-ha-existing.sh\033[0m"
  exit 0
fi
