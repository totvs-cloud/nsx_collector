#!/usr/bin/env bash
# Atualiza nsx-collector e aci-collector na dev-redes (o que estiver instalado).
# Uso: sudo bash /home/nsx_collector/scripts/update-collectors.sh
#
# Detecta automaticamente o que tem em /home/nsx_collector e /home/aci_collector,
# faz git pull, rebuild, restart e mostra status.

set -uo pipefail

# Go pode estar fora do PATH do root
export PATH="$PATH:/usr/local/go/bin"

# ---- helpers ----
log()  { echo -e "\n\033[1;36m==> $*\033[0m"; }
ok()   { echo -e "    \033[0;32m[OK]\033[0m $*"; }
err()  { echo -e "    \033[0;31m[ERRO]\033[0m $*"; }
warn() { echo -e "    \033[0;33m[WARN]\033[0m $*"; }

update_collector() {
    local name="$1"      # nsx-collector ou aci-collector
    local dir="$2"       # /home/nsx_collector ou /home/aci_collector
    local build_path="$3"  # ./cmd/ ou ./cmd/aci-collector/

    if [ ! -d "$dir/.git" ]; then
        warn "$name nao instalado em $dir, pulando"
        return 0
    fi

    log "Atualizando $name"
    cd "$dir" || { err "cd $dir falhou"; return 1; }

    if ! git pull; then
        err "git pull falhou em $dir"
        return 1
    fi

    if ! CGO_ENABLED=0 go build -o "/tmp/$name" "$build_path"; then
        err "build de $name falhou"
        return 1
    fi
    ok "build concluido"

    systemctl stop "$name" 2>/dev/null || true
    mv -f "/tmp/$name" "$dir/$name"
    chmod +x "$dir/$name"
    systemctl start "$name"

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
    err "rode como root (sudo)"
    exit 1
fi

log "Versao do Go"
go version || { err "go nao encontrado"; exit 1; }

update_collector "nsx-collector" "/home/nsx_collector" "./cmd/"
update_collector "aci-collector" "/home/aci_collector" "./cmd/aci-collector/"

log "Concluido"
