#!/usr/bin/env bash
# Deploy nsx-collector em 10.100.29.200 (TESP03)
# Rodar de dev-redes. Usa expect (sem necessidade de sshpass).
set -euo pipefail

REMOTE_HOST="10.100.29.200"
REMOTE_USER="admin"
REMOTE_PASS='nSx--T@!@dm!n#nsxT@2!'
REMOTE_DIR="/home/nsx_collector"
LOCAL_BIN="/home/nsx_collector/nsx-collector"
INFLUX_TOKEN="FLKJPw-nIgGobRHwhGH2KGVRaoYRvWiMqBuzLqZa8I_La1q2K7Nz_ruSvX1m0wMSW0eFlFo1KpMYer1T6NAz7A=="

# Funções auxiliares usando expect
ssh_cmd() {
  expect -c "
    spawn ssh -o StrictHostKeyChecking=no ${REMOTE_USER}@${REMOTE_HOST} \"$1\"
    expect {password:} { send \"${REMOTE_PASS}\r\" }
    expect eof
  "
}

scp_file() {
  expect -c "
    spawn scp -o StrictHostKeyChecking=no $1 ${REMOTE_USER}@${REMOTE_HOST}:$2
    expect {password:} { send \"${REMOTE_PASS}\r\" }
    expect eof
  "
}

echo "==> Criando arquivos de configuração..."
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
  - site: "TESP3"
    url: "https://10.100.29.200"
    user_env: "NSX_TESP3_USER"
    password_env: "NSX_TESP3_PASS"
    tls_skip_verify: true
    enabled: true
EOF

cat > "$TMPDIR/.env" <<EOF
INFLUX_TOKEN=${INFLUX_TOKEN}
NSX_TESP3_USER=admin
NSX_TESP3_PASS='nSx--T@!@dm!n#nsxT@2!'
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

echo "==> Criando diretórios remotos..."
ssh_cmd "mkdir -p ${REMOTE_DIR}/configs"

echo "==> Enviando binário e configs..."
scp_file "$LOCAL_BIN"              "${REMOTE_DIR}/nsx-collector"
scp_file "$TMPDIR/.env"            "${REMOTE_DIR}/.env"
scp_file "$TMPDIR/config.yaml"     "${REMOTE_DIR}/configs/config.yaml"
scp_file "$TMPDIR/managers.yaml"   "${REMOTE_DIR}/configs/managers.yaml"
scp_file "$TMPDIR/nsx-collector.service" "/tmp/nsx-collector.service"

echo "==> Instalando e iniciando serviço..."
expect -c "
  spawn ssh -o StrictHostKeyChecking=no ${REMOTE_USER}@${REMOTE_HOST}
  expect {password:} { send \"${REMOTE_PASS}\r\" }
  expect {\\\$} {
    send \"chmod +x ${REMOTE_DIR}/nsx-collector\r\"
    send \"chmod 600 ${REMOTE_DIR}/.env\r\"
    send \"cp /tmp/nsx-collector.service /etc/systemd/system/nsx-collector.service\r\"
    send \"systemctl daemon-reload\r\"
    send \"systemctl enable nsx-collector\r\"
    send \"systemctl restart nsx-collector\r\"
    send \"sleep 3 && systemctl status nsx-collector --no-pager\r\"
    send \"exit\r\"
  }
  expect eof
"

echo ""
echo "==> Deploy concluído em ${REMOTE_HOST}."
echo "    Logs: ssh ${REMOTE_USER}@${REMOTE_HOST} 'journalctl -u nsx-collector -n 50'"
