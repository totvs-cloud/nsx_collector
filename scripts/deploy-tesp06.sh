#!/usr/bin/env bash
# Deploy nsx-collector em 10.114.36.200 (TESP6)
# Rodar de dev-redes onde o binário já existe.
set -euo pipefail

REMOTE_HOST="10.114.36.200"
REMOTE_USER="admin"
REMOTE_PASS='nSx--T@!@dm!n#nsxT@2!'
REMOTE_DIR="/home/nsx_collector"
LOCAL_BIN="/home/nsx_collector/nsx-collector"
INFLUX_TOKEN="FLKJPw-nIgGobRHwhGH2KGVRaoYRvWiMqBuzLqZa8I_La1q2K7Nz_ruSvX1m0wMSW0eFlFo1KpMYer1T6NAz7A=="

echo "==> Criando arquivos de configuracao..."
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

cat > "$TMPDIR/config.yaml" <<'EOF'
influxdb:
  url: "http://10.114.35.75:8086"
  org: "TOTVS"
  bucket: "nsx"

logging:
  level: "info"
  format: "json"

telemetry:
  enabled: true
  address: ":9101"

intervals:
  default: 40s
  traffic: 15s
EOF

cat > "$TMPDIR/managers.yaml" <<'EOF'
managers:
  - site: "TESP6"
    url: "https://10.114.36.200"
    user_env: "NSX_TESP6_USER"
    password_env: "NSX_TESP6_PASS"
    tls_skip_verify: true
    enabled: true
EOF

cat > "$TMPDIR/.env" <<EOF
INFLUX_TOKEN=${INFLUX_TOKEN}
NSX_TESP6_USER=admin
NSX_TESP6_PASS='nSx--T@!@dm!n#nsxT@2!'
EOF

cat > "$TMPDIR/nsx-collector.service" <<'EOF'
[Unit]
Description=NSX Collector
After=network.target

[Service]
Type=simple
ExecStart=/home/nsx_collector/nsx-collector \
  -config /home/nsx_collector/configs/config.yaml \
  -managers /home/nsx_collector/configs/managers.yaml \
  -env-file /home/nsx_collector/.env
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

echo "==> Enviando arquivos para ${REMOTE_HOST}..."
sshpass -p "$REMOTE_PASS" ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" \
  "mkdir -p ${REMOTE_DIR}/configs"

sshpass -p "$REMOTE_PASS" scp -o StrictHostKeyChecking=no \
  "$LOCAL_BIN" \
  "$TMPDIR/.env" \
  "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DIR}/"

sshpass -p "$REMOTE_PASS" scp -o StrictHostKeyChecking=no \
  "$TMPDIR/config.yaml" \
  "$TMPDIR/managers.yaml" \
  "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_DIR}/configs/"

sshpass -p "$REMOTE_PASS" scp -o StrictHostKeyChecking=no \
  "$TMPDIR/nsx-collector.service" \
  "${REMOTE_USER}@${REMOTE_HOST}:/tmp/nsx-collector.service"

echo "==> Configurando servico em ${REMOTE_HOST}..."
sshpass -p "$REMOTE_PASS" ssh -o StrictHostKeyChecking=no "${REMOTE_USER}@${REMOTE_HOST}" bash <<'REMOTE'
set -e
chmod +x /home/nsx_collector/nsx-collector
chmod 600 /home/nsx_collector/.env
cp /tmp/nsx-collector.service /etc/systemd/system/nsx-collector.service
systemctl daemon-reload
systemctl enable nsx-collector
systemctl restart nsx-collector
sleep 3
systemctl status nsx-collector --no-pager
REMOTE

echo ""
echo "==> Deploy concluido em ${REMOTE_HOST}."
echo "    Logs: ssh ${REMOTE_USER}@${REMOTE_HOST} journalctl -u nsx-collector -f"
