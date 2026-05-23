#!/usr/bin/env bash
# Gera /etc/check_mk/mrpe.cfg.d/nsx-ha-<site>.cfg a partir dos T0 edge clusters
# atualmente descobertos pelo nsx-collector.
#
# Idempotente: pode rodar quantas vezes quiser. Reescreve apenas os arquivos
# nsx-ha-<site>.cfg, sem tocar em outros arquivos do mrpe.cfg.d.
#
# Pré-requisitos:
#   - nsx-collector instalado em /home/nsx_collector com configs/managers.yaml
#   - /opt/nsx-collector/checkmk-nsx-ha.sh instalado (copiado deste repo)
#   - check_mk_agent configurado para ler /etc/check_mk/mrpe.cfg.d/
#
# Uso:
#   sudo bash scripts/generate-mrpe-ha.sh
#
# Variáveis de ambiente (opcionais):
#   COLLECTOR_BIN  default: /home/nsx_collector/nsx-collector
#   CONFIG_FILE    default: /home/nsx_collector/configs/config.yaml
#   MANAGERS_FILE  default: /home/nsx_collector/configs/managers.yaml
#   ENV_FILE       default: /home/nsx_collector/.env
#   MRPE_DIR       default: /etc/check_mk/mrpe.cfg.d
#   SCRIPT_PATH    default: /opt/nsx-collector/checkmk-nsx-ha.sh

set -uo pipefail

COLLECTOR_BIN="${COLLECTOR_BIN:-/home/nsx_collector/nsx-collector}"
CONFIG_FILE="${CONFIG_FILE:-/home/nsx_collector/configs/config.yaml}"
MANAGERS_FILE="${MANAGERS_FILE:-/home/nsx_collector/configs/managers.yaml}"
ENV_FILE="${ENV_FILE:-/home/nsx_collector/.env}"
MRPE_DIR="${MRPE_DIR:-/etc/check_mk/mrpe.cfg.d}"
SCRIPT_PATH="${SCRIPT_PATH:-/opt/nsx-collector/checkmk-nsx-ha.sh}"

log()  { echo -e "\n\033[1;36m==> $*\033[0m"; }
ok()   { echo -e "    \033[0;32m[OK]\033[0m $*"; }
err()  { echo -e "    \033[0;31m[ERRO]\033[0m $*" >&2; }
warn() { echo -e "    \033[0;33m[WARN]\033[0m $*" >&2; }

if [ "$(id -u)" != "0" ]; then
    err "rode como root (sudo) — precisa escrever em ${MRPE_DIR}"
    exit 1
fi

for f in "$COLLECTOR_BIN" "$CONFIG_FILE" "$MANAGERS_FILE"; do
    if [ ! -e "$f" ]; then
        err "$f não existe — o nsx-collector está instalado?"
        exit 1
    fi
done

if [ ! -x "$SCRIPT_PATH" ]; then
    warn "$SCRIPT_PATH ainda não está instalado/executável."
    warn "Copie de scripts/checkmk-nsx-ha.sh deste repo antes de habilitar no checkmk:"
    warn "  install -m 0755 nsx-collector/scripts/checkmk-nsx-ha.sh ${SCRIPT_PATH}"
fi

mkdir -p "$MRPE_DIR"

log "Consultando T0 clusters via collector"
CLUSTERS_JSON=$("$COLLECTOR_BIN" \
    -config "$CONFIG_FILE" \
    -managers "$MANAGERS_FILE" \
    -env-file "$ENV_FILE" \
    -print-clusters 2>/tmp/nsx-print-clusters.err)
RC=$?
if [ "$RC" -ne 0 ] || [ -z "$CLUSTERS_JSON" ]; then
    err "falha em -print-clusters (exit ${RC})"
    cat /tmp/nsx-print-clusters.err >&2
    exit 1
fi

# Agrupa por site, gera um nsx-ha-<site>.cfg por site
mapfile -t SITES < <(echo "$CLUSTERS_JSON" | python3 -c '
import json,sys
d=json.load(sys.stdin)
sites=sorted({c["site"] for c in d})
print("\n".join(sites))
')

if [ "${#SITES[@]}" -eq 0 ]; then
    err "nenhum T0 cluster retornado"
    exit 1
fi

TOTAL=0
for SITE in "${SITES[@]}"; do
    SITE_SAFE=$(echo "$SITE" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9' '_' | sed 's/_*$//')
    OUT_FILE="${MRPE_DIR}/nsx-ha-${SITE_SAFE}.cfg"

    log "Gerando ${OUT_FILE}"

    # Cabeçalho + linhas (1 por T0 cluster do site)
    LINES=$(echo "$CLUSTERS_JSON" | python3 -c '
import json, sys, re
site=sys.argv[1]
script=sys.argv[2]
d=json.load(sys.stdin)
n=0
for c in d:
    if c["site"] != site:
        continue
    # nome seguro pro mrpe service name (sem espaços nem caracteres exóticos)
    safe=re.sub(r"[^A-Za-z0-9_.-]", "_", c["t0_display_name"])
    name=f"NSX_HA_{safe}"
    print(f"{name} {script} {site} {c[\"t0_cluster_id\"]} \"{c[\"t0_display_name\"]}\"")
    n+=1
import sys as _s
print(f"# total: {n}", file=_s.stderr)
' "$SITE" "$SCRIPT_PATH" 2>/tmp/_count)
    COUNT=$(awk '{print $3}' /tmp/_count)

    {
        echo "# nsx-ha-${SITE_SAFE}.cfg — gerado por generate-mrpe-ha.sh em $(date -Iseconds)"
        echo "# Site: ${SITE} — ${COUNT} T0 edge clusters"
        echo "# NÃO EDITAR manualmente: rode 'sudo bash scripts/generate-mrpe-ha.sh' pra regerar."
        echo "$LINES"
    } > "$OUT_FILE"

    ok "${OUT_FILE} (${COUNT} entradas)"
    TOTAL=$((TOTAL + COUNT))
done

log "Concluído: ${TOTAL} entradas mrpe geradas em ${#SITES[@]} site(s)"
echo "    (o check_mk_agent fará pickup automático na próxima execução)"
