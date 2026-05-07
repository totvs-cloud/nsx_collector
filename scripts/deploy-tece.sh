#!/usr/bin/env bash
# Instalacao do nsx-collector na VM do TECE.
# Rodar direto na VM: sudo bash deploy-tece.sh
set -euo pipefail

REPO_URL="git@github.com:totvs-cloud/nsx_collector.git"
INSTALL_DIR="/home/nsx_collector"
GO_VERSION="1.23.6"

SITE_NAME="TECE"
NSX_URL="https://172.18.214.31"
NSX_USER="admin"
NSX_PASS='$hak@d3V!rg&m'
INFLUX_URL="http://10.114.35.75:8086"
INFLUX_TOKEN="FLKJPw-nIgGobRHwhGH2KGVRaoYRvWiMqBuzLqZa8I_La1q2K7Nz_ruSvX1m0wMSW0eFlFo1KpMYer1T6NAz7A=="

SITE_ENV="NSX_${SITE_NAME}_USER"
SITE_ENV_PASS="NSX_${SITE_NAME}_PASS"

# ── 1. Instalar Go se nao existir ────────────────────────────────────────────
if ! command -v go &>/dev/null && [ ! -x /usr/local/go/bin/go ]; then
  echo "==> Instalando Go ${GO_VERSION}..."
  curl -sSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tar.gz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tar.gz
  rm /tmp/go.tar.gz
  echo 'export PATH="/usr/local/go/bin:$PATH"' > /etc/profile.d/go.sh
fi
export PATH="/usr/local/go/bin:$PATH"
echo "    Go: $(go version)"

# ── 2. Clonar repositorio ────────────────────────────────────────────────────
echo "==> Clonando repositorio..."
BUILD_DIR="/tmp/nsx-collector-src"
rm -rf "$BUILD_DIR"
git clone -b master "$REPO_URL" "$BUILD_DIR"

# ── 3. Compilar ──────────────────────────────────────────────────────────────
echo "==> Compilando..."
cd "$BUILD_DIR"
go build -o /tmp/nsx-collector ./cmd/
echo "    OK"

# ── 4. Instalar binario e configs ────────────────────────────────────────────
echo "==> Instalando em ${INSTALL_DIR}..."
mkdir -p "${INSTALL_DIR}/configs"
cp /tmp/nsx-collector "${INSTALL_DIR}/nsx-collector"
chmod +x "${INSTALL_DIR}/nsx-collector"

cat > "${INSTALL_DIR}/configs/config.yaml" <<EOF
influxdb:
  url: "${INFLUX_URL}"
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

cat > "${INSTALL_DIR}/configs/managers.yaml" <<EOF
managers:
  - site: "${SITE_NAME}"
    url: "${NSX_URL}"
    user_env: "${SITE_ENV}"
    password_env: "${SITE_ENV_PASS}"
    tls_skip_verify: true
    enabled: true
EOF

cat > "${INSTALL_DIR}/.env" <<EOF
INFLUX_TOKEN=${INFLUX_TOKEN}
${SITE_ENV}=${NSX_USER}
${SITE_ENV_PASS}='${NSX_PASS}'
EOF
chmod 600 "${INSTALL_DIR}/.env"

# ── 5. Servico systemd ───────────────────────────────────────────────────────
cat > /etc/systemd/system/nsx-collector.service <<EOF
[Unit]
Description=NSX Collector
After=network.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/nsx-collector \\
  -config ${INSTALL_DIR}/configs/config.yaml \\
  -managers ${INSTALL_DIR}/configs/managers.yaml \\
  -env-file ${INSTALL_DIR}/.env
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable nsx-collector
systemctl restart nsx-collector
sleep 3
systemctl status nsx-collector --no-pager

rm -rf "$BUILD_DIR" /tmp/nsx-collector
echo ""
echo "==> Instalacao concluida."
echo "    Logs: journalctl -u nsx-collector -f"
