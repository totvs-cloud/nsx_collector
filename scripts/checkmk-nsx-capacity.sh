#!/bin/bash
# Checkmk local check - NSX Edge Bandwidth Utilization
# Instalar em: /usr/lib/check_mk_agent/local/checkmk-nsx-capacity.sh
#
# Consulta InfluxDB por utilização média dos últimos 5min do measurement nsx_edge_bandwidth.
# Gera checks com performance data no formato Checkmk:
#   P "service name" metric=value;warn;crit;min;max text
#
# Thresholds: warning=70%, critical=90%

INFLUX_URL="${INFLUX_URL:-http://10.114.35.75:8086}"
INFLUX_ORG="${INFLUX_ORG:-TOTVS}"
INFLUX_BUCKET="${INFLUX_BUCKET:-nsx}"
INFLUX_TOKEN="${INFLUX_TOKEN}"

WARN=70
CRIT=90

if [ -z "$INFLUX_TOKEN" ] && [ -f /opt/nsx-collector/.influx_token ]; then
    INFLUX_TOKEN=$(cat /opt/nsx-collector/.influx_token)
fi

if [ -z "$INFLUX_TOKEN" ]; then
    echo "3 \"NSX Edge Capacity\" - UNKNOWN: INFLUX_TOKEN not set"
    exit 0
fi

QUERY='
from(bucket: "'"${INFLUX_BUCKET}"'")
  |> range(start: -5m)
  |> filter(fn: (r) => r._measurement == "nsx_edge_bandwidth")
  |> filter(fn: (r) => r._field == "rx_utilization_pct" or r._field == "tx_utilization_pct" or r._field == "link_speed_mbps" or r._field == "rx_bps" or r._field == "tx_bps")
  |> group(columns: ["node_name", "interface_id", "_field"])
  |> mean()
  |> pivot(rowKey: ["node_name", "interface_id"], columnKey: ["_field"], valueColumn: "_value")
'

RESULT=$(curl -s --request POST \
    "${INFLUX_URL}/api/v2/query?org=${INFLUX_ORG}" \
    --header "Authorization: Token ${INFLUX_TOKEN}" \
    --header "Content-Type: application/vnd.flux" \
    --header "Accept: text/csv" \
    --data-raw "${QUERY}" 2>/dev/null)

if [ $? -ne 0 ] || [ -z "$RESULT" ]; then
    echo "3 \"NSX Edge Capacity\" - UNKNOWN: InfluxDB query failed"
    exit 0
fi

echo "$RESULT" | tail -n +2 | while IFS=',' read -r _ _ _ _ _ _ iface_id node_name link_speed rx_bps rx_util tx_bps tx_util _rest; do
    [ -z "$node_name" ] && continue
    [ "$node_name" = "" ] && continue

    # Remove quotes
    node_name=$(echo "$node_name" | tr -d '"')
    iface_id=$(echo "$iface_id" | tr -d '"')

    # Format values as integers for display
    rx_util_int=$(printf "%.1f" "$rx_util" 2>/dev/null || echo "0.0")
    tx_util_int=$(printf "%.1f" "$tx_util" 2>/dev/null || echo "0.0")
    link_speed_val=$(printf "%.0f" "$link_speed" 2>/dev/null || echo "0")

    # Short node name (remove domain)
    short_name=$(echo "$node_name" | cut -d. -f1)

    service_name="NSX BW ${short_name} ${iface_id}"

    echo "P \"${service_name}\" rx_util=${rx_util_int};${WARN};${CRIT};0;100|tx_util=${tx_util_int};${WARN};${CRIT};0;100 RX ${rx_util_int}% TX ${tx_util_int}% (${link_speed_val}M link)"
done
