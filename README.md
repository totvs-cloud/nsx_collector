# NSX Edge Bandwidth Monitor

Coletor de telemetria para NSX Edge nodes com alertas de capacidade via Slack.

## Funcionalidades

- Coleta de métricas via NSX Manager REST API (cluster, transport nodes, uplinks, LB, alarms)
- Cálculo de bandwidth rate no collector (sem depender de `derivative()` no Grafana)
- Utilização % por interface com link speed real (coletado via SSH `get physical-port`)
- Alertas Slack com screenshot do Grafana quando utilização atinge threshold
- Dashboard Grafana dedicado (NSX Edge Bandwidth)
- Local check para Checkmk

## Arquitetura

```
NSX Manager (REST API)
    ↓ (40s)
nsx-collector (Go)
    ├── RateCalculator → nsx_edge_bandwidth (InfluxDB)
    ├── Alerting → Slack (quando util >= 90%)
    │   ├── Mensagem com edge, interface, erros, link Grafana
    │   └── Screenshot RX Utilization (Grafana Render API)
    └── Contadores cumulativos → nsx_edge_uplink (InfluxDB)
                                      ↓
                                  Grafana Dashboards
```

## Alerta Slack

Quando a utilização de qualquer interface atinge o threshold:

- **WARNING** (>= 90%): alerta amarelo
- **CRITICAL** (>= 99%): alerta vermelho

Cada alerta inclui:
- Edge node e interface afetada
- Throughput atual vs capacidade do link
- Taxa de erros RX/TX
- Link direto para o dashboard Grafana filtrado
- Screenshot do painel RX Utilization anexado na thread

## Sites Monitorados

| Site | Edges | Link Speed |
|------|-------|------------|
| TESP2 | Edge-017 a 024, edg1p00001-002 | 10G/25G misto |
| TESP3 | edg1p00001-002 (25G), edg1p00004-008 (10G), edg1p00009-011 (10G Mellanox) | 10G/25G |
| TESP4 | edg1p00001-002 (25G), edg1p00005-006 (25G Mellanox) | 25G |
| TESP5 | edg1p00001-008 (25G Mellanox) | 25G |
| TESP6 | edg1p00001-008 (25G) | 25G |

## Deploy

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o nsx-collector ./cmd/
systemctl restart nsx-collector
```

## Configuração

```yaml
# config.yaml
slack:
  enabled: true
  bot_token_env: "SLACK_BOT_TOKEN"
  channel: "C_CANAL_ID"
  grafana_url: "http://grafana:3000"
  dashboard_url: "http://grafana-publico:3000/d/uid/dashboard"
  rx_util_panel_id: "5"
```
