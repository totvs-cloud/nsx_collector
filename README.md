# NSX Collector

Coletor Go de telemetria NSX-T para o ambiente TOTVS Cloud. Roda como serviço systemd em uma VM dev-redes por site, conversa com o NSX Manager via REST API, calcula derivadas (bandwidth rate) localmente, persiste tudo em InfluxDB v2 e dispara alertas Slack + checks MRPE no checkmk.

Escopo:
- **7 sites em produção**: TECE, TESP2, TESP3, TESP4, TESP5, TESP6, TESP7
- **Coleta multi-frequência**: 4 ciclos (15s / 40s / 1m / 5m) sem trambolho de cron
- **Forense HA**: persiste histórico de quem é ACTIVE em cada SR (o NSX não persiste isso)
- **Alertas acionáveis**: Slack com screenshot do Grafana, MRPE pro checkmk

---

## Sumário

- [Funcionalidades](#funcionalidades)
- [Arquitetura](#arquitetura)
- [Ciclos de coleta](#ciclos-de-coleta)
- [Coleta HA (failover forensics)](#coleta-ha-failover-forensics)
- [Measurements no InfluxDB](#measurements-no-influxdb)
- [Endpoints NSX consumidos](#endpoints-nsx-consumidos)
- [Alertas Slack](#alertas-slack)
- [Telemetria Prometheus](#telemetria-prometheus)
- [Configuração](#configuração)
- [Scripts](#scripts)
- [Sites monitorados](#sites-monitorados)
- [Dashboards Grafana](#dashboards-grafana)
- [Build e CLI](#build-e-cli)
- [Stack](#stack)

---

## Funcionalidades

| Domínio | O que faz | Output |
|---------|-----------|--------|
| **Bandwidth & rate** | Polla contadores de RX/TX bytes por interface uplink, calcula bps + utilização % no collector (sem `derivative()` no Grafana), com detecção de stale read e counter wrap | `nsx_edge_bandwidth`, `nsx_edge_uplink` |
| **Cluster & Manager health** | Status do cluster (mgmt/control/overall), uptime por Manager, contagem online/offline | `nsx_cluster`, `nsx_manager` |
| **Edge nodes** | PNIC up/down, tunnel/BFD, CPU DPDK, memória system/datapath, disk, load avg, uptime | `nsx_transport_node`, `nsx_edge_resource` |
| **Logical routers** | Inventário T0/T1/VRF + link T1→T0, edge_cluster_id, status | `nsx_logical_router` |
| **HA monitoring** | 1 ciclo de 1min observa 10 T1s por edge cluster e detecta failover por regra de maioria | `nsx_ha_state`, `nsx_ha_cluster_summary`, `nsx_ha_change` |
| **Alarms** | Alarmes abertos do NSX por severidade | `nsx_alarms` |
| **Capacity** | Capacity usage report (NSServices, interfaces, etc.) — bucket separado | `nsx_capacity` |
| **Load Balancer** | LB services, virtual servers, pools, status runtime | `nsx_lb_service`, `nsx_lb_virtual_server`, `nsx_lb_pool` |
| **Alerting** | Threshold 90/99% com cooldown + screenshot Grafana anexado | Slack |
| **MRPE** | Checks locais que consultam Influx pra integração com checkmk | exit 0/1/2 |

---

## Arquitetura

```
                  ┌──────────────────────────────────────────────┐
                  │            NSX Manager (REST API)            │
                  │   /api/v1/* (legacy) + /policy/api/v1/*      │
                  └──────────────────┬───────────────────────────┘
                                     │ Basic Auth + retry 429
                  ┌──────────────────▼───────────────────────────┐
                  │             nsx-collector (Go)               │
                  │                                              │
                  │  ┌──────────── scheduler.go ──────────────┐  │
                  │  │   tick a cada intervals.default (40s)  │  │
                  │  └────────────────┬───────────────────────┘  │
                  │                   │                          │
                  │   ┌───────────────▼────────────────┐         │
                  │   │       worker.Collect()         │         │
                  │   │  ─────────────────────────────  │         │
                  │   │  every tick (40s) │  default   │         │
                  │   │  every 15s        │  traffic   │         │
                  │   │  gated 1m         │  ha        │         │
                  │   │  gated 5m         │  slow      │         │
                  │   └───┬────────────┬────────┬──────┘         │
                  │       │            │        │                │
                  │   ┌───▼───┐  ┌─────▼────┐  ┌▼──────────┐    │
                  │   │ rate  │  │ ha       │  │ alerting  │    │
                  │   │ calc  │  │ collector│  │ evaluator │    │
                  │   └───┬───┘  └─────┬────┘  └─────┬─────┘    │
                  │       │            │             │           │
                  │   ┌───▼────────────▼─────────────▼──────┐    │
                  │   │       influxdb writer (batch)       │    │
                  │   └────────────────┬────────────────────┘    │
                  │                    │                         │
                  │   ┌────────────────▼─────────┐  ┌─────────┐  │
                  │   │  Prometheus :9101/metrics│  │ Slack   │  │
                  │   └──────────────────────────┘  └─────────┘  │
                  └──────────────────────────────────────────────┘
                                     │
                  ┌──────────────────▼───────────────────────────┐
                  │   InfluxDB v2 (buckets: nsx, nsx_capacity)   │
                  └──────────────────┬───────────────────────────┘
                                     │
                  ┌──────────────────▼───────────────────────────┐
                  │   Grafana  +  checkmk MRPE                   │
                  └──────────────────────────────────────────────┘
```

Cada site tem sua **própria VM dev-redes** com 1 instância isolada do nsx-collector falando apenas com o NSX Manager local. Nenhum cross-site.

---

## Ciclos de coleta

| Ciclo | Intervalo padrão | O que faz |
|-------|------------------|-----------|
| **default** | 40s | cluster status, transport_nodes (status + interfaces), logical routers, manager uptime |
| **traffic** | 15s (subset do default) | bandwidth — lê contadores RX/TX por interface e gera bps via `RateCalculator` |
| **ha** | 1m (gated) | coleta HA per-T1 dos 10 observados por edge cluster, detecta failover |
| **slow** | 5m (gated) | alarms (status=OPEN), capacity usage, NS services count, load balancer (services + VS + pools) |

O scheduler dispara um único tick a cada `default`; gates internos (`lastSlow`, `lastHA`) decidem se rodam ou não. Sem cron, sem race condition.

---

## Coleta HA (failover forensics)

**Problema que resolve:** o NSX Manager **não persiste** eventos de troca de papel ACTIVE/STANDBY de Service Routers. Quando um edge cai e o tráfego migra, não tem como auditar depois. Endpoint `/policy/api/v1/.../service-router-cluster/status` retorna 404 nessa versão.

**Como funciona:**

1. A cada 1 min, pra cada **edge cluster** (T0), o collector mantém um inventário de **10 T1s observados** persistido em `state_dir/ha-watch-<site>.json`
2. Pra cada T1 observado, chama `GET /api/v1/logical-routers/<UUID>/status` (endpoint legacy ainda funciona) e lê `per_node_status[].high_availability_status`
3. Calcula o **consensus ACTIVE** (transport_node mais frequente entre os 10)
4. Compara com o ciclo anterior (`prevActive` em memória). Se ≥ ⌈observed/2⌉ T1s trocaram de ACTIVE no mesmo ciclo → grava `nsx_ha_change` (= **failover real**)
5. Pra clusters com `observed < 3`, fallback "qualquer mudança = evento"

**Modos de inventário** (`managers.yaml`, por manager):
- `auto` (default): sorteia 10 T1s vivos com `crypto/rand` na 1ª execução, persiste, mantém
- `pinned`: usa só os nomes em `t1_names` que existirem no cluster (pra T1s estratégicos: DB-Shared, etc.)
- `hybrid`: pinned primeiro, completa até `size` com auto

**Healing:** se um T1 observado some do listing por 2 ciclos consecutivos (deletado, renomeado, 404), é tirado e substituído por outro sorteado entre os vivos. Substituição **não gera CRIT** — só log + counter Prometheus.

**Restart do collector** zera `prevActive` em memória — o 1º ciclo pós-restart só baselina, não emite evento. Comportamento aceitável.

Detalhes: [docs/HA-COLLECTION.md](docs/HA-COLLECTION.md).

---

## Measurements no InfluxDB

Bucket `nsx` (default):

| Measurement | Tags principais | Fields principais |
|-------------|-----------------|-------------------|
| `nsx_manager` | site, manager_id, manager_ip | uptime_ms |
| `nsx_cluster` | site, cluster_id | mgmt_status, control_status, overall_status, online_nodes, offline_nodes |
| `nsx_transport_node` | site, node_id, node_name, node_type | status, pnic_up/down, tunnel_up/down, bfd_*, mgmt_conn, control_conn |
| `nsx_edge_resource` | site, node_id, node_name | cpu_dpdk_*, mem_system_pct, mem_datapath_pct, disk_used_pct, load_avg_*, uptime_ms |
| `nsx_logical_router` | site, router_id, router_name, router_type, parent_t0 | edge_cluster_id, status |
| `nsx_edge_uplink` | site, node_id, node_name, interface_id, interface_type, phy_name | rx_bytes, tx_bytes, rx_packets, tx_packets, rx_errors, tx_errors, link_speed_mbps |
| `nsx_edge_bandwidth` | site, node_id, node_name, interface_id | rx_bps, tx_bps, rx_util_pct, tx_util_pct, link_speed_mbps |
| `nsx_alarms` | site, alarm_id, severity, entity_type | entity_id, description |
| `nsx_lb_service` / `nsx_lb_virtual_server` / `nsx_lb_pool` | site, *_id, *_name | status, contadores |
| `nsx_ha_state` | site, t0_cluster_id, t0_name, t1_id, t1_name, transport_node_id, transport_node_name, ha_state | state_num (2=ACTIVE, 1=STANDBY, 0=DOWN/SYNC) |
| `nsx_ha_cluster_summary` | site, t0_cluster_id, t0_name, consensus_node_id, consensus_node_name | observed, consensus_count |
| `nsx_ha_change` | site, t0_cluster_id, t0_name, from_active, to_active, from_active_name, to_active_name | changed_count, observed_count, changed_names |

Bucket `nsx_capacity` (separado):

| Measurement | Tags | Fields |
|-------------|------|--------|
| `nsx_capacity` | site, capacity_type | usage_count, usage_pct |

---

## Endpoints NSX consumidos

Todos via Basic Auth, retry exponencial em 429 honrando `Retry-After`.

| Endpoint | Pra que |
|----------|---------|
| `/api/v1/cluster/status` | uptime e status do cluster |
| `/api/v1/cluster/nodes/<id>/status` | uptime por Manager |
| `/api/v1/transport-nodes` (paginado) | inventário de edge/host TNs |
| `/api/v1/transport-nodes/<id>/status` | PNIC/tunnel/BFD, load avg, CPU/mem/disk |
| `/api/v1/transport-nodes/<id>/network/interfaces` | lista interfaces do edge |
| `/api/v1/transport-nodes/<id>/network/interfaces/<id>/stats` | RX/TX bytes/packets/errors (base do bandwidth) |
| `/api/v1/logical-routers` (paginado) | T0/T1/VRF |
| `/api/v1/logical-routers/<id>/status` | **HA state per TN** (base da coleta HA) |
| `/api/v1/logical-router-ports` | resolve link T1→T0 |
| `/api/v1/alarms?status=OPEN` | alarmes abertos |
| `/api/v1/capacity/usage` | capacity report |
| `/api/v1/ns-services?page_size=1` | contagem de NS services |
| `/api/v1/loadbalancer/{services,virtual-servers,pools}` | metadata LB |
| `/api/v1/loadbalancer/services/<id>/status` | runtime LB |

---

## Alertas Slack

- **Trigger**: utilização RX ou TX de qualquer interface uplink ≥ 90% (WARNING) ou ≥ 99% (CRITICAL)
- **Dedup**: cooldown de 3 min por `(node_name, interface_id)` pra não inundar
- **Mensagem**: edge, interface, throughput vs capacidade, taxa de erros RX/TX, link Grafana filtrado
- **Anexo**: screenshot do painel RX/TX Utilization via Grafana Render API postado na thread

Config em `config.yaml` → `slack:` (token via env, channel ID, grafana URL público, panel IDs).

---

## Telemetria Prometheus

Servidor em `:9101/metrics` (configurável):

| Métrica | Tipo | Labels |
|---------|------|--------|
| `nsx_collector_collect_cycles_total` | counter | site |
| `nsx_collector_collect_duration_seconds` | histogram | site |
| `nsx_collector_collect_errors_total` | counter | site, component |
| `nsx_collector_points_written_total` | counter | site |
| `nsx_collector_ha_polls_total` | counter | site |
| `nsx_collector_ha_changes_total` | counter | site, t0_cluster |
| `nsx_collector_ha_observed_t1s` | gauge | site, t0_cluster |
| `nsx_collector_ha_watch_substitutions_total` | counter | site, t0_cluster |

---

## Configuração

### `configs/config.yaml`

```yaml
influxdb:
  url: "http://10.114.35.75:8086"
  org: "TOTVS"
  bucket: "nsx"
  capacity_bucket: "nsx_capacity"
  token_env: "INFLUX_TOKEN"

logging:
  level: info
  format: json

telemetry:
  enabled: true
  address: ":9101"

intervals:
  default: 40s
  traffic: 15s
  slow: 5m          # alarms, capacity, LB
  ha: 1m            # coleta HA per-T1

slack:
  enabled: true
  bot_token_env: "SLACK_BOT_TOKEN"
  channel: "C_CANAL_ID"
  grafana_url: "http://grafana:3000"
  dashboard_url: "http://grafana-publico:3000/d/uid/dashboard"
  grafana_key_env: "GRAFANA_API_KEY"
  rx_util_panel_id: "5"

# Override quando a API NSX retorna link_speed=0 (Mellanox antigas, etc.)
interface_speed_overrides:
  tesp3edg1p00009:
    "eth0": 10000
```

### `configs/managers.yaml`

```yaml
managers:
  - site: "TESP3"
    url: "https://10.100.29.200"
    user_env: "NSX_TESP3_USER"
    password_env: "NSX_TESP3_PASS"
    tls_skip_verify: true
    enabled: true
    state_dir: /home/nsx_collector/state
    ha_watch:
      mode: auto        # auto | pinned | hybrid
      size: 10
      t1_names: []      # usado em pinned/hybrid
```

Múltiplos managers no mesmo arquivo são suportados (cada um vira um worker independente).

### `.env`

```bash
INFLUX_TOKEN=...
NSX_TESP3_USER=admin
NSX_TESP3_PASS=...
SLACK_BOT_TOKEN=xoxb-...
GRAFANA_API_KEY=...
```

---

## Scripts

```
scripts/
├── deploy-tece.sh, deploy-tesp02..07.sh   Deploy inicial por site
├── setup.sh                               Bootstrap standalone (sem .git)
├── update-collectors.sh                   Update incremental em VMs com .git
├── upgrade-ha-existing.sh                 Upgrade HA cirúrgico (backup + rollback automático)
├── preflight-check.sh                     Validação read-only pré-upgrade (8 seções)
├── generate-mrpe-ha.sh                    Gera /etc/check_mk/mrpe.cfg.d/nsx-ha-<site>.cfg
├── checkmk-nsx-ha.sh                      MRPE check pra failover HA (lê Influx)
├── checkmk-nsx-capacity.sh                MRPE check pra capacity
├── tshoot-rate.sh                         Debug rate calculation (2 leituras com 30s)
└── nsx-collector.service                  Systemd unit
```

### Fluxo recomendado pra adicionar HA em VM em produção

```bash
# 1. Valida (não muda nada)
sudo bash preflight-check.sh

# 2. Se 0 FAIL, aplica (backup + rollback auto se algo der errado)
sudo bash upgrade-ha-existing.sh

# 3. Valida pós-deploy
journalctl -u nsx-collector | grep -iE 'ha|failover' | tail
cat /home/nsx_collector/state/ha-watch-*.json | jq '.clusters | keys'
```

---

## Sites monitorados

| Site | NSX Manager | Edge clusters | Highlights |
|------|-------------|---------------|------------|
| TECE | — | 2 | edg1p00001-002 |
| TESP2 | — | 1 | edg1p00001-002, Edge-017 a 024 (10G/25G misto) |
| TESP3 | 10.100.29.200 | 5 | edg1p00001-011 (25G/10G/Mellanox) — 400 T1s |
| TESP4 | — | 2 | edg1p00001-002, edg1p00005-006 (Mellanox) |
| TESP5 | 10.108.36.200 | 4 | edg1p00001-008 (25G Mellanox) — 400 T1s |
| TESP6 | 10.114.36.200 | 2 | edg1p00001-008 (25G) |
| TESP7 | — | 2 | + TESP7-INFRABASE como 2º manager |

Cada site tem 4 edge clusters × 10 T1s = ~40 T1s sob observação HA em média.

---

## Dashboards Grafana

- **`dashboards/nsx-ha-tatico.json`** — Dashboard tático HA (16 painéis):
  - Contador de mudanças HA (24h)
  - T1s observados ACTIVE vs Consenso (com destaque de outliers)
  - Estado bruto T1×TN
  - Timeline de ACTIVE consensus por edge cluster
  - Heatmap por T1 selecionado (ACTIVE=2 / STANDBY=1)
  - Distribuição de ACTIVE entre edge nodes
  - Histograma de eventos por hora
  - Annotation `HA Change Events` que marca failovers em todos os timeseries
  - Variáveis: `$site`, `$edge_cluster`, `$t1_name`

Outros dashboards (NSX Edge Bandwidth, NSX Capacity, etc.) vivem no Grafana e não são versionados neste repo.

---

## Build e CLI

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o nsx-collector ./cmd/
systemctl restart nsx-collector
```

Flags:

| Flag | Default | O que faz |
|------|---------|-----------|
| `-config` | `/home/nsx_collector/configs/config.yaml` | path do config.yaml |
| `-managers` | `configs/managers.yaml` | path do managers.yaml |
| `-env-file` | `.env` | path do .env |
| `--print-clusters` | — | imprime JSON `[{site, t0_cluster_id, t0_display_name}]` e sai. Usado por `generate-mrpe-ha.sh` |

Graceful shutdown em SIGINT/SIGTERM — aguarda goroutines drenarem antes de sair.

---

## Stack

- **Go 1.23**
- `github.com/influxdata/influxdb-client-go/v2` — InfluxDB v2 client
- `github.com/joho/godotenv` — carrega .env
- `github.com/prometheus/client_golang` — métricas Prometheus
- `go.uber.org/zap` — structured logging (JSON ou console)
- `gopkg.in/yaml.v3` — config YAML

Sem CGO, binário estático único de ~15 MB.

---

## Histórico relevante

- **Coleta HA (mai/2026)** adicionada após incidente em `tesp6edg1p00002` (22/05/2026) — relatório executivo identificou gap: NSX não persiste eventos HA. A nova coleta resolve isso pra auditoria forense futura.
- **Inventário com healing** preferido a "alarme por T1" porque um failover real migra todos os T1s do cluster juntos — 10 observados por cluster basta pra detectar (e evita N alarmes redundantes no checkmk).
