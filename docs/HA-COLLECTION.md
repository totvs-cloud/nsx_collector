# NSX HA Collection — coleta de Active/Standby de Service Routers

Este módulo coleta, a cada **1 minuto**, o papel HA (ACTIVE/STANDBY) dos
**Service Routers** de T0/T1 nos edge clusters NSX-T monitorados pelo
`nsx-collector`. O objetivo é preencher o gap exposto pelo incidente de
2026-05-22 (`coletadotemp/Relatorio-Executivo-Migracao-SRs-Edge-TESP6 (2).docx`):
o NSX Manager não persiste histórico de troca de papel HA de SR, e os
endpoints `/policy/api/v1/.../locale-services/<ls>/state` retornam **404**
nesta versão. O endpoint **legacy** `/api/v1/logical-routers/<UUID>/status`
funciona e devolve `per_node_status[]` com `high_availability_status`
(ACTIVE/STANDBY/DOWN/SYNC/UNKNOWN) por transport node.

## Como funciona

```
┌────────────────────────────────────────────────────────────┐
│  Worker (a cada 40s, gating de 1m pra HA)                  │
│                                                            │
│  1. GET /api/v1/logical-routers       → T0s + T1s + ECID   │
│  2. agrupa T1s por edge_cluster_id                         │
│  3. carrega inventário (auto/pinned/hybrid)                │
│     /home/nsx_collector/state/ha-watch-<site>.json         │
│  4. healing: se T1 some, substitui após 2 ciclos           │
│  5. paralelo (sem 4): GET /logical-routers/<id>/status     │
│     pra cada T1 observado                                  │
│  6. consensus ACTIVE por cluster (moda dos transport nodes)│
│  7. diff vs prev em memória; emite change SE ≥ maioria     │
│     dos observados mudou (ceil(N/2))                       │
│  8. escreve no InfluxDB (bucket nsx)                       │
└────────────────────────────────────────────────────────────┘
```

## Measurements (bucket `nsx`)

### `nsx_ha_state`
Estado bruto por (T1, transport_node) a cada ciclo. **É o ouro forense** —
permite responder "em 22/05 às 16:25 quem migrou e pra onde".

| Tags | Fields |
|---|---|
| `site`, `t0_cluster_id`, `t0_name`, `t1_id`, `t1_name`, `transport_node_id`, `ha_state` | `state_num` (int: ACTIVE=2, STANDBY=1, outros=0) |

### `nsx_ha_cluster_summary`
Agregado por T0 edge cluster: qual é o ACTIVE majoritário e quantos dos
observados concordam.

| Tags | Fields |
|---|---|
| `site`, `t0_cluster_id`, `t0_name`, `consensus_node_id` | `observed`, `consensus_count`, `outliers` |

### `nsx_ha_change` (evento)
**Só é escrito** quando ≥ ceil(observed/2) dos T1s observados mudaram
ACTIVE entre 2 ciclos consecutivos. É o feed que o MRPE consome.

| Tags | Fields |
|---|---|
| `site`, `t0_cluster_id`, `t0_name`, `from_active`, `to_active` | `changed_count`, `observed_count`, `changed_names` (csv até 500 chars) |

## Inventário de T1s observados

10 T1s por T0 edge cluster (configurável via `ha_watch.size`).

Arquivo persistido em **`/home/nsx_collector/state/ha-watch-<site>.json`**.

`managers.yaml`:
```yaml
ha_watch:
  mode: auto          # auto | pinned | hybrid (default: auto)
  size: 10
  t1_names: []        # usado em pinned/hybrid (display_name exato)
```

- **auto**: na 1ª execução sorteia `size` T1s vivos por cluster (crypto/rand),
  persiste, mantém. Zero manutenção.
- **pinned**: usa só os nomes em `t1_names` que existirem no cluster. Bom pra
  T1s "estratégicos" (DB-Shared etc.).
- **hybrid**: começa pelos `t1_names` válidos, completa o resto até `size`
  com auto.

**Healing**: se um T1 observado for 404/sumir do listing por 2 ciclos
consecutivos, é removido do estado e substituído por outro sorteado entre
os T1s vivos do mesmo cluster que ainda não estão na lista. Substituição
gera log + counter Prometheus, **não** vira CRIT no checkmk.

Resetar o inventário (re-sortear tudo do zero):
```bash
rm /home/nsx_collector/state/ha-watch-<site>.json
systemctl restart nsx-collector
```

## Dashboard tático

`dashboards/nsx-ha-tatico.json` (uid: `nsx-ha-tatico`, title: "NSX HA —
Tactical"). Variáveis: `$site`, `$t0_cluster`, `$t1_name`. Panels:

| # | Painel | O que mostra |
|---|---|---|
| 1 | stat — Mudanças HA (24h) por cluster | contagem de `nsx_ha_change` em 24h |
| 2 | stat — T1s observados (filtrados) | soma de `observed` na última leitura |
| 3 | stat — Outliers ativos | soma de `outliers` na última leitura |
| 4 | table — Snapshot por T0 cluster | site, T0, edge ACTIVE consensus, observados, outliers |
| 5 | state-timeline — ACTIVE consensus por cluster | troca de cor = failover de cluster |
| 6 | table — Eventos de Failover | últimas trocas detectadas com from→to e nomes |
| 7 | barchart — Eventos por hora | histograma agregado dos `nsx_ha_change` |
| 8 | table — Estado por T1 observado | 1 linha por (T1, transport_node) na última coleta |
| 9 | timeseries — T1 selecionado | foca num T1 (via $t1_name), pula 2↔1 a cada troca |

Annotation: eventos `nsx_ha_change` viram marcadores **vermelhos** em
todos os painéis de série temporal.

Importar no Grafana: **Dashboards → New → Import → Upload JSON file**.

## Estrutura de alarme (MRPE) — pronta mas opt-in

Tudo está pronto pra ligar o checkmk, mas o disparo de alertas está
**segurado** (decisão do time). Quando habilitar:

```bash
# 1. instalar o local check em /opt/nsx-collector/
sudo install -m 0755 nsx-collector/scripts/checkmk-nsx-ha.sh /opt/nsx-collector/checkmk-nsx-ha.sh

# 2. gerar mrpe.cfg.d/nsx-ha-<site>.cfg (1 linha por T0 cluster)
sudo bash nsx-collector/scripts/generate-mrpe-ha.sh
# → /etc/check_mk/mrpe.cfg.d/nsx-ha-tesp3.cfg (5 linhas pra TESP3, 1 por T0)

# 3. checkmk agent faz pickup automático no próximo poll
```

Daí em diante, cada execução do `update-collectors.sh` regenera o mrpe.cfg.
Sem o local check instalado, esse passo vira **no-op**.

### Semântica CRIT/OK no checkmk

- **CRIT (exit 2)** se há `nsx_ha_change` para (site, T0 cluster) nos
  últimos **90s**. Mensagem inclui `from_active → to_active` e nomes dos
  T1s afetados. Texto usa a **Opção B** do plano (assertiva, com IDs).
- **OK (exit 0)** caso contrário, com estado normal extraído de
  `nsx_ha_cluster_summary` (último 5min): "ACTIVE consensus em
  `<edge>` (`<n>/<N>` T1s observados; outliers=`<x>`)".
- **UNKNOWN (exit 3)** se não há dados de `nsx_ha_cluster_summary` em
  5min (sintoma: collector parou no site).

Vida útil curta por design: vira CRIT na 1ª leitura pós-evento, volta a
OK na próxima leitura (~60s) se não houver novo evento. Sem ack manual.

## Telemetria (Prometheus, porta `:9101`)

| Métrica | Tipo | Labels |
|---|---|---|
| `nsx_collector_ha_polls_total` | counter | `site` |
| `nsx_collector_ha_changes_total` | counter | `site`, `t0_cluster` |
| `nsx_collector_ha_observed_t1s` | gauge | `site`, `t0_cluster` |
| `nsx_collector_ha_watch_substitutions_total` | counter | `site`, `t0_cluster` |

## Limitações conhecidas

- **Restart zera prevActive em memória** — 1º ciclo após restart só
  baselina (não emite change events). Aceitável; histórico vivo no Influx
  preserva o estado anterior se for preciso reconstruir manualmente.
- **`/policy/.../state` 404**: confirmado nessa versão. Usamos só a API
  `/api/v1/logical-routers/<UUID>/status`.
- **`per_node_status` vazio**: T1s sem SR (sem edge_cluster_id) não geram
  pontos. O collector já filtra antes da chamada (`EdgeClusterID == ""`).
- **Sub-clusters NSX**: o campo `is_default_sub_cluster` é capturado mas
  não usado nas regras de consensus. Se você tem clusters com sub-cluster
  ativo, considere `ha_watch.mode: pinned` pra escolher T1s do
  sub-cluster certo.

## Comandos úteis de verificação

```bash
# inventário atual do site
cat /home/nsx_collector/state/ha-watch-tesp3.json | jq '.clusters[] | {t0_name, observed_count: (.observed|length)}'

# logs HA do worker
journalctl -u nsx-collector -f | grep -iE 'ha:|failover'

# influx — todas as séries HA do site (últimos 5m)
influx query 'from(bucket:"nsx") |> range(start:-5m) |> filter(fn:(r) => r._measurement =~ /nsx_ha_.*/ and r.site=="TESP3") |> count()'

# rodar o MRPE script manualmente (sem instalar)
INFLUX_TOKEN=... bash nsx-collector/scripts/checkmk-nsx-ha.sh TESP3 53129cbd-... "T0-Cluster_1"

# imprimir T0 clusters atuais (consumido pelo generate-mrpe-ha.sh)
/home/nsx_collector/nsx-collector \
  -config /home/nsx_collector/configs/config.yaml \
  -managers /home/nsx_collector/configs/managers.yaml \
  -env-file /home/nsx_collector/.env \
  -print-clusters | jq .
```
