#!/usr/bin/env bash
# Atualiza nsx-collector e aci-collector na dev-redes — clone fresh em /tmp,
# build, substitui só o binário no diretório de instalação, restart.
# Não depende de /home/X_collector/.git existir.
#
# Uso (rode como root):
#   sudo bash setup.sh

set -uo pipefail

export PATH="$PATH:/usr/local/go/bin"

NSX_REPO="git@github.com:totvs-cloud/nsx_collector.git"
ACI_REPO="git@github.com:totvs-cloud/aci_collector.git"

log()  { echo -e "\n\033[1;36m==> $*\033[0m"; }
ok()   { echo -e "    \033[0;32m[OK]\033[0m $*"; }
err()  { echo -e "    \033[0;31m[ERRO]\033[0m $*"; }
warn() { echo -e "    \033[0;33m[WARN]\033[0m $*"; }

update() {
    local name="$1"           # nsx-collector | aci-collector
    local install_dir="$2"    # /home/nsx_collector | /home/aci_collector
    local repo="$3"           # git@... do GitHub
    local build_path="$4"     # ./cmd/ | ./cmd/aci-collector/

    if [ ! -d "$install_dir" ]; then
        warn "$install_dir nao existe, pulando $name"
        return 0
    fi

    log "Atualizando $name"
    local tmp="/tmp/${name}-build"
    rm -rf "$tmp"

    if ! git clone --depth 1 -b master "$repo" "$tmp" 2>&1; then
        err "clone falhou: $repo (branch master)"
        return 1
    fi

    if ! (cd "$tmp" && CGO_ENABLED=0 go build -o "/tmp/${name}.bin" "$build_path"); then
        err "build de $name falhou"
        return 1
    fi
    ok "build concluido"

    systemctl stop "$name" 2>/dev/null || true
    mv -f "/tmp/${name}.bin" "${install_dir}/${name}"
    chmod +x "${install_dir}/${name}"
    systemctl start "$name"

    rm -rf "$tmp"

    sleep 2
    if systemctl is-active --quiet "$name"; then
        ok "$name rodando"
        echo
        journalctl -u "$name" -n 5 --no-pager
    else
        err "$name nao subiu apos restart"
        journalctl -u "$name" -n 20 --no-pager
        return 1
    fi
}

if [ "$(id -u)" != "0" ]; then
    err "rode como root (sudo bash setup.sh)"
    exit 1
fi

log "Versao do Go"
go version || { err "go nao encontrado em PATH:/usr/local/go/bin"; exit 1; }

update "nsx-collector" "/home/nsx_collector" "$NSX_REPO" "./cmd/"
update "aci-collector" "/home/aci_collector" "$ACI_REPO" "./cmd/aci-collector/"

log "Concluido"
