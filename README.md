# NSX Collector

Coletor de telemetria para NSX-T (edge nodes, T0/T1, HA, capacity, LB) com export pra InfluxDB, alertas Slack e checks MRPE.

## Funcionalidades

- **Bandwidth & utilização** — rate calculado no collector (sem `derivative()` no Grafana), link speed real via SSH `get physical-port`
- **HA monitoring** — coleta a cada 1 min do estado ACTIVE/STANDBY dos SRs (Service Routers) de 10 T1s observados por edge cluster, com detecção de failover por regra de maioria
- **Capacity & alarms** — coleta NSX alarms e métricas de capacidade
- **LB stats** — pools, virtual servers, status
- **Slack alerting** — alertas com screenshot do Grafana quando utilização atinge threshold
- **MRPE/Checkmk** — checks via Influx pra integrar com monitoramento corporativo

## Arquitetura

```
NSX Manager (REST API)
    ↓
nsx-collector (Go)
    ├── default (40s)   → cluster, transport_nodes
    ├── traffic (15s)   → bandwidth + rate calc → nsx_edge_bandwidth
    ├── slow    (5m)    → alarms, capacity, LB
    └── ha      (1m)    → HA state (10 T1s/cluster, majority-rule failover)
                          ↓
                       InfluxDB (bucket: nsx)
                          ↓
                       Grafana + Checkmk MRPE + Slack
```

Measurements gravados:
- `nsx_edge_bandwidth` — throughput/utilização por interface
- `nsx_edge_uplink` — contadores cumulativos
- `nsx_capacity` — capacity metrics
- `nsx_ha_state` — estado HA per-T1×TN (forense)
- `nsx_ha_cluster_summary` — consensus ACTIVE por edge cluster
- `nsx_ha_change` — eventos de failover (só dispara com regra de maioria)

## Coleta HA

A cada 1 minuto, pra cada edge cluster (T0), o collector observa 10 T1s representativos e checa qual transport node está ACTIVE pra cada um via `GET /api/v1/logical-routers/<UUID>/status`. Quando ≥ metade dos observados trocam de ACTIVE no mesmo ciclo (regra de maioria), grava um `nsx_ha_change` no Influx.

Inventário dos 10 T1s vive em `state_dir/ha-watch-<site>.json` (default `/home/nsx_collector/state/`). Modos: `auto` (sorteio crypto/rand + healing automático se T1 sumir), `pinned` (lista fixa), `hybrid`.

Detalhes completos: [docs/HA-COLLECTION.md](docs/HA-COLLECTION.md).

## Sites Monitorados

Cada site tem sua própria VM dev-redes com 1 instância do nsx-collector:

| Site | NSX Manager | Edge clusters | Edges (highlights) |
|------|-------------|---------------|---------------------|
| TECE | 10.108.x.x | 2 | edg1p00001-002 |
| TESP2 | — | 1 | edg1p00001-002, Edge-017 a 024 |
| TESP3 | 10.100.29.200 | 5 | edg1p00001-011 (25G/10G/Mellanox) |
| TESP4 | — | 2 | edg1p00001-002, edg1p00005-006 (Mellanox) |
| TESP5 | 10.108.36.200 | 4 | edg1p00001-008 (25G Mellanox) |
| TESP6 | 10.114.36.200 | 2 | edg1p00001-008 (25G) |
| TESP7 | — | 2 | + TESP7-INFRABASE manager |

## Scripts

```
scripts/
├── deploy-tese.sh         # deploy inicial (1 manager)
├── deploy-tesp0{2..7}.sh  # deploy inicial por site (managers + creds inline)
├── setup.sh               # bootstrap standalone (não depende de .git)
├── update-collectors.sh   # update incremental em VMs com .git
├── upgrade-ha-existing.sh # upgrade HA cirúrgico (backup + rollback automático)
├── preflight-check.sh     # validação READ-ONLY pré-upgrade
├── generate-mrpe-ha.sh    # gera mrpe.cfg.d/nsx-ha-<site>.cfg
├── checkmk-nsx-capacity.sh
├── checkmk-nsx-ha.sh      # MRPE check pra failover HA
└── tshoot-rate.sh
```

**Fluxo recomendado pra adicionar HA em VM já em produção:**
```bash
# 1. valida ambiente (read-only, não muda nada)
sudo bash preflight-check.sh

# 2. se 0 FAIL, aplica upgrade (com backup + rollback auto)
sudo bash upgrade-ha-existing.sh

# 3. valida estado HA pós-deploy
journalctl -u nsx-collector | grep -iE 'ha|failover' | tail
cat /home/nsx_collector/state/ha-watch-*.json | jq '.clusters | keys'
```

## Dashboards

- `dashboards/nsx-ha-tatico.json` — Grafana tactical dashboard pra HA (16 panels: failover counter, snapshot, timeline per edge cluster, heatmap por T1, outliers, etc.). Variáveis: `$site`, `$edge_cluster`, `$t1_name`. Annotation `HA Change Events` marca failovers no tempo.

## Configuração

```yaml
# configs/config.yaml
intervals:
  default: 40s
  traffic: 15s
  slow: 5m       # alarms, capacity, LB
  ha: 1m         # coleta HA per-T1

slack:
  enabled: true
  bot_token_env: "SLACK_BOT_TOKEN"
  channel: "C_CANAL_ID"
  grafana_url: "http://grafana:3000"
  dashboard_url: "http://grafana-publico:3000/d/uid/dashboard"
  rx_util_panel_id: "5"
```

```yaml
# configs/managers.yaml
managers:
  - site: "TESP3"
    url: "https://10.100.29.200"
    user_env: "NSX_TESP3_USER"
    password_env: "NSX_TESP3_PASS"
    enabled: true
    state_dir: /home/nsx_collector/state
    ha_watch:
      mode: auto         # auto | pinned | hybrid
      size: 10
      t1_names: []       # usado em pinned/hybrid
```

## Build

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o nsx-collector ./cmd/
systemctl restart nsx-collector
```

Flags:
- `-config <path>` — config.yaml (default `configs/config.yaml`)
- `-managers <path>` — managers.yaml (default `configs/managers.yaml`)
- `-env-file <path>` — credenciais (default `.env`)
- `--print-clusters` — imprime JSON com `[{site, t0_cluster_id, t0_display_name}, ...]` e sai (usado pelo `generate-mrpe-ha.sh`)

## Alertas Slack

Quando a utilização de qualquer interface atinge o threshold:

- **WARNING** (≥ 90%) — alerta amarelo
- **CRITICAL** (≥ 99%) — alerta vermelho

Cada alerta inclui edge/interface, throughput vs capacidade, taxa de erros RX/TX, link Grafana filtrado e screenshot do painel RX Utilization anexado na thread.
