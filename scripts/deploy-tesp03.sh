#!/usr/bin/env bash
# Deploy nsx-collector em 10.100.29.200 (TESP03)
# Roda do zero no dev-redes: clona repo, compila e instala no tesp03.
set -euo pipefail

REMOTE_HOST="10.100.29.200"
REMOTE_USER="admin"
REMOTE_PASS='nSx--T@!@dm!n#nsxT@2!'
REMOTE_DIR="/home/nsx_collector"
REPO_URL="git@github.com:totvs-cloud/nsx_collector.git"
BUILD_DIR="/tmp/nsx-collector-build"
INFLUX_TOKEN="FLKJPw-nIgGobRHwhGH2KGVRaoYRvWiMqBuzLqZa8I_La1q2K7Nz_ruSvX1m0wMSW0eFlFo1KpMYer1T6NAz7A=="

scp_file() {
  expect -c "
    spawn scp -o StrictHostKeyChecking=no \"$1\" ${REMOTE_USER}@${REMOTE_HOST}:\"$2\"
    expect -re {[Pp]assword:} { send \"${REMOTE_PASS}\r\" }
    expect eof
  "
}

ssh_run() {
  expect -c "
    spawn ssh -o StrictHostKeyChecking=no ${REMOTE_USER}@${REMOTE_HOST} \"$1\"
    expect -re {[Pp]assword:} { send \"${REMOTE_PASS}\r\" }
    expect eof
  "
}

# ── 1. Clonar / atualizar repo ──────────────────────────────────────────────
echo "==> Clonando repositório..."
if [ -d "$BUILD_DIR/.git" ]; then
  git -C "$BUILD_DIR" pull
else
  rm -rf "$BUILD_DIR"
  git clone "$REPO_URL" "$BUILD_DIR"
fi

# ── 2. Compilar binário ──────────────────────────────────────────────────────
echo "==> Compilando binário Linux amd64..."
cd "$BUILD_DIR"
GOOS=linux GOARCH=amd64 go build -o /tmp/nsx-collector ./cmd/
echo "    OK: /tmp/nsx-collector"

# ── 3. Criar arquivos de configuração ───────────────────────────────────────
echo "==> Criando arquivos de configuração..."
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR" /tmp/nsx-collector' EXIT

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

# ── 4. Enviar arquivos ───────────────────────────────────────────────────────
echo "==> Criando diretórios em ${REMOTE_HOST}..."
ssh_run "mkdir -p ${REMOTE_DIR}/configs"

echo "==> Enviando arquivos..."
scp_file "/tmp/nsx-collector"          "${REMOTE_DIR}/nsx-collector"
scp_file "$TMPDIR/.env"                "${REMOTE_DIR}/.env"
scp_file "$TMPDIR/config.yaml"         "${REMOTE_DIR}/configs/config.yaml"
scp_file "$TMPDIR/managers.yaml"       "${REMOTE_DIR}/configs/managers.yaml"
scp_file "$TMPDIR/nsx-collector.service" "/tmp/nsx-collector.service"

# ── 5. Instalar e iniciar serviço ────────────────────────────────────────────
echo "==> Instalando serviço em ${REMOTE_HOST}..."
expect -c "
  set timeout 30
  spawn ssh -o StrictHostKeyChecking=no ${REMOTE_USER}@${REMOTE_HOST}
  expect -re {[Pp]assword:} { send \"${REMOTE_PASS}\r\" }
  expect -re {[#\$]} {
    send \"chmod +x ${REMOTE_DIR}/nsx-collector && chmod 600 ${REMOTE_DIR}/.env\r\"
    expect -re {[#\$]}
    send \"cp /tmp/nsx-collector.service /etc/systemd/system/nsx-collector.service\r\"
    expect -re {[#\$]}
    send \"systemctl daemon-reload && systemctl enable nsx-collector && systemctl restart nsx-collector\r\"
    expect -re {[#\$]}
    send \"sleep 3 && systemctl status nsx-collector --no-pager\r\"
    expect -re {[#\$]}
    send \"exit\r\"
  }
  expect eof
"

echo ""
echo "==> Deploy concluído em ${REMOTE_HOST}."
echo "    Logs: ssh ${REMOTE_USER}@${REMOTE_HOST} 'journalctl -u nsx-collector -n 50'"
