#!/bin/bash
# Checkmk MRPE check — NSX HA failover per T0 edge cluster
#
# Estrutura de alarme está pronta, mas o rollout para o agente checkmk fica em
# espera (segurar a criação de alertas). Quando habilitar:
#   1. Copie este script para /opt/nsx-collector/checkmk-nsx-ha.sh
#   2. Rode scripts/generate-mrpe-ha.sh para gerar
#      /etc/check_mk/mrpe.cfg.d/nsx-ha-<site>.cfg
#   3. Restart do check_mk_agent (ou aguarde o pickup automático).
#
# Uso (uma linha do mrpe.cfg):
#   NSX_HA_T0_Cluster_1 /opt/nsx-collector/checkmk-nsx-ha.sh <site> <t0_cluster_id> "<t0_display_name>"
#
# Semântica:
#   Consulta o measurement nsx_ha_change no InfluxDB nos últimos 90s. Se houver
#   evento para (site, t0_cluster_id) → CRIT (exit 2). Se não → OK (exit 0)
#   com estado atual derivado de nsx_ha_cluster_summary (last 5m).
#
# Output: 1 linha de status checkmk no formato:
#   <status_code> "<service_name>" <perfdata> <text>

set -uo pipefail

SITE="${1:-}"
T0_CLUSTER_ID="${2:-}"
T0_NAME="${3:-$T0_CLUSTER_ID}"

if [ -z "$SITE" ] || [ -z "$T0_CLUSTER_ID" ]; then
    echo "3 \"NSX HA\" - UNKNOWN: usage: $0 <site> <t0_cluster_id> [<t0_display_name>]"
    exit 0
fi

SERVICE_NAME="NSX HA ${SITE} ${T0_NAME}"

INFLUX_URL="${INFLUX_URL:-http://10.114.35.75:8086}"
INFLUX_ORG="${INFLUX_ORG:-TOTVS}"
INFLUX_BUCKET="${INFLUX_BUCKET:-nsx}"
INFLUX_TOKEN="${INFLUX_TOKEN:-}"

if [ -z "$INFLUX_TOKEN" ] && [ -f /opt/nsx-collector/.influx_token ]; then
    INFLUX_TOKEN=$(cat /opt/nsx-collector/.influx_token)
fi
if [ -z "$INFLUX_TOKEN" ] && [ -f /home/nsx_collector/.env ]; then
    INFLUX_TOKEN=$(grep -E '^INFLUX_TOKEN=' /home/nsx_collector/.env | head -1 | cut -d= -f2- | tr -d '"' | tr -d "'")
fi
if [ -z "$INFLUX_TOKEN" ]; then
    echo "3 \"${SERVICE_NAME}\" - UNKNOWN: INFLUX_TOKEN not set"
    exit 0
fi

query_influx() {
    local query="$1"
    curl -s --max-time 10 --request POST \
        "${INFLUX_URL}/api/v2/query?org=${INFLUX_ORG}" \
        --header "Authorization: Token ${INFLUX_TOKEN}" \
        --header "Content-Type: application/vnd.flux" \
        --header "Accept: text/csv" \
        --data-raw "${query}" 2>/dev/null
}

# 1) Janela curta — qualquer change event nos últimos 90s para este cluster?
#    Limita ao último ponto e mantém os tags from_active, to_active.
CHANGE_QUERY='
from(bucket: "'"${INFLUX_BUCKET}"'")
  |> range(start: -90s)
  |> filter(fn: (r) => r._measurement == "nsx_ha_change")
  |> filter(fn: (r) => r.site == "'"${SITE}"'" and r.t0_cluster_id == "'"${T0_CLUSTER_ID}"'")
  |> filter(fn: (r) => r._field == "changed_count" or r._field == "observed_count" or r._field == "changed_names")
  |> last()
  |> pivot(rowKey: ["_time", "from_active", "to_active"], columnKey: ["_field"], valueColumn: "_value")
'

CHANGE_CSV=$(query_influx "${CHANGE_QUERY}")
# Linha de dados (primeira após o header). InfluxDB CSV: header + 1 linha por row.
CHANGE_LINE=$(echo "${CHANGE_CSV}" | awk -F',' 'NR>1 && NF>5 {print; exit}')

if [ -n "${CHANGE_LINE}" ]; then
    # Colunas do CSV pivotado: ,result,table,_time,from_active,to_active,changed_count,changed_names,observed_count
    # Extrair via cut/awk (ordem pode variar conforme order dos fields no pivot — usar nomes do header).
    HEADER=$(echo "${CHANGE_CSV}" | awk 'NR==2{print}')
    get_col() {
        local name="$1"
        echo "${HEADER}" | tr ',' '\n' | grep -nx "${name}" | head -1 | cut -d: -f1
    }
    col_from=$(get_col "from_active")
    col_to=$(get_col "to_active")
    col_changed=$(get_col "changed_count")
    col_observed=$(get_col "observed_count")
    col_names=$(get_col "changed_names")

    FROM_ACTIVE=$(echo "${CHANGE_LINE}"  | cut -d, -f"${col_from}"     | tr -d '"')
    TO_ACTIVE=$(echo   "${CHANGE_LINE}"  | cut -d, -f"${col_to}"       | tr -d '"')
    CHANGED=$(echo     "${CHANGE_LINE}"  | cut -d, -f"${col_changed}"  | tr -d '"')
    OBSERVED=$(echo    "${CHANGE_LINE}"  | cut -d, -f"${col_observed}" | tr -d '"')
    NAMES=$(echo       "${CHANGE_LINE}"  | cut -d, -f"${col_names}"    | tr -d '"' | cut -c1-160)

    : "${CHANGED:=?}"
    : "${OBSERVED:=?}"
    : "${FROM_ACTIVE:=-}"
    : "${TO_ACTIVE:=-}"

    # Opção B (assertiva com IDs) — texto recomendado no plano.
    TEXT="[CRIT] FAILOVER ${T0_NAME} (${SITE}) — ${CHANGED} de ${OBSERVED} T1s monitorados perderam ACTIVE em ${FROM_ACTIVE} e foram reassumidos por ${TO_ACTIVE}. Investigar imediatamente o edge anterior. T1s: ${NAMES}"
    echo "2 \"${SERVICE_NAME}\" changed=${CHANGED};1;1;0;${OBSERVED}|observed=${OBSERVED};;;0; ${TEXT}"
    exit 0
fi

# 2) Sem evento — estado normal. Pega o último summary do cluster (últimos 5m).
SUMMARY_QUERY='
from(bucket: "'"${INFLUX_BUCKET}"'")
  |> range(start: -5m)
  |> filter(fn: (r) => r._measurement == "nsx_ha_cluster_summary")
  |> filter(fn: (r) => r.site == "'"${SITE}"'" and r.t0_cluster_id == "'"${T0_CLUSTER_ID}"'")
  |> filter(fn: (r) => r._field == "observed" or r._field == "consensus_count" or r._field == "outliers")
  |> last()
  |> pivot(rowKey: ["_time", "consensus_node_id"], columnKey: ["_field"], valueColumn: "_value")
'

SUMMARY_CSV=$(query_influx "${SUMMARY_QUERY}")
SUMMARY_LINE=$(echo "${SUMMARY_CSV}" | awk -F',' 'NR>1 && NF>5 {print; exit}')

if [ -z "${SUMMARY_LINE}" ]; then
    echo "3 \"${SERVICE_NAME}\" - UNKNOWN: sem dados de nsx_ha_cluster_summary nos últimos 5m (collector parou?)"
    exit 0
fi

HEADER=$(echo "${SUMMARY_CSV}" | awk 'NR==2{print}')
get_col() {
    local name="$1"
    echo "${HEADER}" | tr ',' '\n' | grep -nx "${name}" | head -1 | cut -d: -f1
}
col_node=$(get_col "consensus_node_id")
col_observed=$(get_col "observed")
col_consensus=$(get_col "consensus_count")
col_outliers=$(get_col "outliers")

NODE=$(echo      "${SUMMARY_LINE}" | cut -d, -f"${col_node}"      | tr -d '"')
OBSERVED=$(echo  "${SUMMARY_LINE}" | cut -d, -f"${col_observed}"  | tr -d '"')
CONSENSUS=$(echo "${SUMMARY_LINE}" | cut -d, -f"${col_consensus}" | tr -d '"')
OUTLIERS=$(echo  "${SUMMARY_LINE}" | cut -d, -f"${col_outliers}"  | tr -d '"')

: "${OBSERVED:=0}"
: "${CONSENSUS:=0}"
: "${OUTLIERS:=0}"
: "${NODE:=-}"

TEXT="OK — ACTIVE consensus em ${NODE} (${CONSENSUS}/${OBSERVED} T1s observados; outliers=${OUTLIERS})"
echo "0 \"${SERVICE_NAME}\" changed=0;1;1;0;${OBSERVED}|observed=${OBSERVED};;;0;|outliers=${OUTLIERS};;;0; ${TEXT}"
exit 0
