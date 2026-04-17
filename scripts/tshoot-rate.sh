#!/usr/bin/env bash
# Troubleshooting script para validar o cálculo de rate do nsx-collector.
# Coleta stats direto da API NSX duas vezes, calcula o rate manualmente
# e compara com o que o Grafana/collector mostra.
#
# Uso: ./tshoot-rate.sh <nsx_url> <user> <pass> <node_name> <interface_id> <link_speed_mbps>
# Exemplo: ./tshoot-rate.sh https://10.114.36.200 admin 'senha' tesp6edg1p00002.tesp6infra.local fp-eth0 25000
set -euo pipefail

NSX_URL="${1:?Uso: $0 <nsx_url> <user> <pass> <node_name> <interface_id> <link_speed_mbps>}"
NSX_USER="${2:?}"
NSX_PASS="${3:?}"
NODE_NAME="${4:?}"
IFACE_ID="${5:?}"
LINK_SPEED_MBPS="${6:?}"

INTERVAL=30  # segundos entre as duas coletas

echo "=============================================="
echo " NSX Rate Troubleshooting"
echo "=============================================="
echo "NSX Manager: $NSX_URL"
echo "Node:        $NODE_NAME"
echo "Interface:   $IFACE_ID"
echo "Link Speed:  ${LINK_SPEED_MBPS} Mbps"
echo "Intervalo:   ${INTERVAL}s"
echo "=============================================="
echo ""

# 1. Descobrir o node_id
echo ">>> 1. Buscando node_id para '$NODE_NAME'..."
NODES_JSON=$(curl -sk -u "${NSX_USER}:${NSX_PASS}" "${NSX_URL}/api/v1/transport-nodes" 2>/dev/null)
NODE_ID=$(echo "$NODES_JSON" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for n in data.get('results', []):
    if n.get('display_name') == '${NODE_NAME}':
        print(n['id'])
        break
" 2>/dev/null || true)

if [ -z "$NODE_ID" ]; then
    echo "ERRO: Node '$NODE_NAME' não encontrado!"
    echo ""
    echo "Nodes disponíveis:"
    echo "$NODES_JSON" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for n in data.get('results', []):
    print(f\"  {n.get('display_name', 'N/A')} -> {n['id']}\")
" 2>/dev/null
    exit 1
fi
echo "   node_id: $NODE_ID"
echo ""

# 2. Listar todas as interfaces do node
echo ">>> 2. Interfaces do node:"
IFACES_JSON=$(curl -sk -u "${NSX_USER}:${NSX_PASS}" "${NSX_URL}/api/v1/transport-nodes/${NODE_ID}/network/interfaces" 2>/dev/null)
echo "$IFACES_JSON" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for iface in data.get('results', []):
    iid = iface.get('interface_id', 'N/A')
    itype = iface.get('interface_type', 'N/A')
    speed = iface.get('link_speed', 0)
    admin = iface.get('admin_status', 'N/A')
    link = iface.get('link_status', 'N/A')
    print(f'  {iid:12s}  type={itype:10s}  speed={speed:>6d} Mbps  admin={admin}  link={link}')
" 2>/dev/null
echo ""

# 3. Primeira coleta de stats
echo ">>> 3. Coleta #1 - stats da interface '$IFACE_ID'..."
T1=$(date +%s%N)
STATS1=$(curl -sk -u "${NSX_USER}:${NSX_PASS}" "${NSX_URL}/api/v1/transport-nodes/${NODE_ID}/network/interfaces/${IFACE_ID}/stats" 2>/dev/null)
echo "   Resposta raw da API:"
echo "$STATS1" | python3 -m json.tool 2>/dev/null || echo "$STATS1"
echo ""

RX1=$(echo "$STATS1" | python3 -c "import json,sys; print(json.load(sys.stdin).get('rx_bytes',0))" 2>/dev/null)
TX1=$(echo "$STATS1" | python3 -c "import json,sys; print(json.load(sys.stdin).get('tx_bytes',0))" 2>/dev/null)
RX_PKT1=$(echo "$STATS1" | python3 -c "import json,sys; print(json.load(sys.stdin).get('rx_packets',0))" 2>/dev/null)
TX_PKT1=$(echo "$STATS1" | python3 -c "import json,sys; print(json.load(sys.stdin).get('tx_packets',0))" 2>/dev/null)

echo "   rx_bytes:   $RX1"
echo "   tx_bytes:   $TX1"
echo "   rx_packets: $RX_PKT1"
echo "   tx_packets: $TX_PKT1"
echo ""

# 4. Esperar intervalo
echo ">>> 4. Aguardando ${INTERVAL}s para segunda coleta..."
sleep "$INTERVAL"

# 5. Segunda coleta
echo ">>> 5. Coleta #2 - stats da interface '$IFACE_ID'..."
T2=$(date +%s%N)
STATS2=$(curl -sk -u "${NSX_USER}:${NSX_PASS}" "${NSX_URL}/api/v1/transport-nodes/${NODE_ID}/network/interfaces/${IFACE_ID}/stats" 2>/dev/null)

RX2=$(echo "$STATS2" | python3 -c "import json,sys; print(json.load(sys.stdin).get('rx_bytes',0))" 2>/dev/null)
TX2=$(echo "$STATS2" | python3 -c "import json,sys; print(json.load(sys.stdin).get('tx_bytes',0))" 2>/dev/null)
RX_PKT2=$(echo "$STATS2" | python3 -c "import json,sys; print(json.load(sys.stdin).get('rx_packets',0))" 2>/dev/null)
TX_PKT2=$(echo "$STATS2" | python3 -c "import json,sys; print(json.load(sys.stdin).get('tx_packets',0))" 2>/dev/null)

echo "   rx_bytes:   $RX2"
echo "   tx_bytes:   $TX2"
echo "   rx_packets: $RX_PKT2"
echo "   tx_packets: $TX_PKT2"
echo ""

# 6. Calcular rates
echo ">>> 6. Cálculo de rate:"
python3 << PYEOF
t1_ns = $T1
t2_ns = $T2
elapsed = (t2_ns - t1_ns) / 1e9

rx1 = $RX1
rx2 = $RX2
tx1 = $TX1
tx2 = $TX2
rx_pkt1 = $RX_PKT1
rx_pkt2 = $RX_PKT2
tx_pkt1 = $TX_PKT1
tx_pkt2 = $TX_PKT2
link_speed_mbps = $LINK_SPEED_MBPS
link_bps = link_speed_mbps * 1_000_000

delta_rx = rx2 - rx1
delta_tx = tx2 - tx1
delta_rx_pkt = rx_pkt2 - rx_pkt1
delta_tx_pkt = tx_pkt2 - tx_pkt1

rx_bytes_sec = delta_rx / elapsed
tx_bytes_sec = delta_tx / elapsed

# Método do collector: assume bytes, multiplica por 8
rx_bps_as_bytes = rx_bytes_sec * 8
tx_bps_as_bytes = tx_bytes_sec * 8
rx_util_as_bytes = (rx_bps_as_bytes / link_bps) * 100 if link_bps > 0 else 0
tx_util_as_bytes = (tx_bps_as_bytes / link_bps) * 100 if link_bps > 0 else 0

# Teste alternativo: e se já forem bits? (sem multiplicar por 8)
rx_bps_as_bits = rx_bytes_sec
tx_bps_as_bits = tx_bytes_sec
rx_util_as_bits = (rx_bps_as_bits / link_bps) * 100 if link_bps > 0 else 0
tx_util_as_bits = (tx_bps_as_bits / link_bps) * 100 if link_bps > 0 else 0

# Tamanho médio de pacote (ajuda a identificar se são bytes ou bits)
avg_rx_pkt_size = delta_rx / delta_rx_pkt if delta_rx_pkt > 0 else 0
avg_tx_pkt_size = delta_tx / delta_tx_pkt if delta_tx_pkt > 0 else 0

print(f"   Elapsed:         {elapsed:.2f}s")
print(f"")
print(f"   Delta RX:        {delta_rx:,} ({delta_rx/1e9:.3f} G)")
print(f"   Delta TX:        {delta_tx:,} ({delta_tx/1e9:.3f} G)")
print(f"   Delta RX pkts:   {delta_rx_pkt:,}")
print(f"   Delta TX pkts:   {delta_tx_pkt:,}")
print(f"")
print(f"   Avg RX pkt size: {avg_rx_pkt_size:.0f} (se bytes: ~64-9000 normal)")
print(f"   Avg TX pkt size: {avg_tx_pkt_size:.0f} (se bits:  ~512-72000)")
print(f"")
print(f"   === Se rx_bytes = BYTES (como o collector assume) ===")
print(f"   RX: {rx_bps_as_bytes/1e9:.2f} Gbps  ({rx_util_as_bytes:.1f}%)")
print(f"   TX: {tx_bps_as_bytes/1e9:.2f} Gbps  ({tx_util_as_bytes:.1f}%)")
print(f"")
print(f"   === Se rx_bytes = BITS (sem *8) ===")
print(f"   RX: {rx_bps_as_bits/1e9:.2f} Gbps  ({rx_util_as_bits:.1f}%)")
print(f"   TX: {tx_bps_as_bits/1e9:.2f} Gbps  ({tx_util_as_bits:.1f}%)")
print(f"")

# Veredicto
if 64 <= avg_rx_pkt_size <= 9000 or 64 <= avg_tx_pkt_size <= 9000:
    print(f"   >>> VEREDICTO: Tamanho médio de pacote sugere que rx_bytes são BYTES")
    print(f"       O collector está correto em multiplicar por 8")
elif avg_rx_pkt_size > 9000 or avg_tx_pkt_size > 9000:
    print(f"   >>> VEREDICTO: Tamanho médio de pacote MUITO ALTO - rx_bytes pode ser BITS!")
    print(f"       O collector NÃO deveria multiplicar por 8")

# Comparação com Grafana
print(f"")
print(f"   === Comparação ===")
print(f"   Se o Grafana mostra ~{rx_util_as_bits:.0f}% RX, os contadores são BITS (remover *8)")
print(f"   Se o Grafana mostra ~{rx_util_as_bytes:.0f}% RX, os contadores são BYTES (manter *8)")
PYEOF

echo ""

# 7. Verificar logs do collector
echo ">>> 7. Últimos logs do collector (rate debug):"
journalctl -u nsx-collector --since "5 min ago" --no-pager 2>/dev/null | grep "rate debug" | grep "$IFACE_ID" | tail -5 || echo "   Nenhum log de rate debug encontrado"
echo ""

# 8. Verificar se rx_bytes == tx_bytes (indicaria contador combinado)
echo ">>> 8. Verificação de contadores duplicados:"
python3 << PYEOF
rx1 = $RX1; tx1 = $TX1
rx2 = $RX2; tx2 = $TX2
delta_rx = rx2 - rx1
delta_tx = tx2 - tx1
ratio = delta_rx / delta_tx if delta_tx > 0 else 0

if abs(delta_rx - delta_tx) < max(delta_rx, delta_tx) * 0.01:
    print(f"   ALERTA: rx e tx delta são quase iguais! ({delta_rx:,} vs {delta_tx:,})")
    print(f"   Isso pode indicar que a API retorna o MESMO contador para ambos (rx+tx combinado)")
else:
    print(f"   RX/TX ratio: {ratio:.3f} (valores diferentes = contadores separados, OK)")
    print(f"   Delta RX: {delta_rx:,}")
    print(f"   Delta TX: {delta_tx:,}")
PYEOF

echo ""
echo "=============================================="
echo " Fim do troubleshooting"
echo "=============================================="
