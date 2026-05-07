# Fix capacity bucket no nsx-collector — TESP7

Contexto: o coletor do TESP7 está gravando capacity no bucket errado (ou
com config antiga). O Influx em `10.114.35.75:8086` agora tem um bucket
dedicado `nsx_capacity`, e queremos alinhar o coletor a escrever lá.

A intenção é que TODOS os sites usem o mesmo bucket `nsx_capacity` pra
capacity (o resto continua no bucket `nsx`).

## O que você (Claude Code) deve fazer

### 1. Diagnóstico inicial

Roda:

```bash
cat /home/nsx_collector/configs/config.yaml
sudo journalctl -u nsx-collector --since "10 min ago" | grep -i 'capacity\|error\|warn' | tail -40
ls -la /home/nsx_collector/nsx-collector
```

Reporta:
- Se já existe `capacity_bucket:` no `config.yaml` (e qual valor)
- Se há erro `bucket "nsx_capacity" not found` nos logs
- Se há erro `bucket "nsx" not found` ou similar
- Mtime do binário (se for de antes de 2026-05-07 está desatualizado)

### 2. Garantir que o binário está atualizado

Se o binário for anterior a 2026-05-07 ~15:00 BRT, recompila:

```bash
cd /tmp && rm -rf nsx-collector-src && \
git clone -b master git@github.com:totvs-cloud/nsx_collector.git nsx-collector-src && \
cd nsx-collector-src && \
go build -o /home/nsx_collector/nsx-collector ./cmd/
```

(Se `go` não estiver no PATH, tenta `/usr/local/go/bin/go` ou `/usr/bin/go`.)

A última master (commit `fc4a39c` ou superior) tem o fix do uptime do
Manager que precisa estar no binário.

### 3. Ajustar o config.yaml

Garante que o `influxdb` block fica assim:

```yaml
influxdb:
  url: "http://10.114.35.75:8086"
  org: "TOTVS"
  bucket: "nsx"
  capacity_bucket: "nsx_capacity"
```

Se a chave `capacity_bucket` já existir com outro valor (ex.: `nsx-capacity`,
`nsx_cap`, etc.), corrige pra `nsx_capacity` exato.

Se não existir, adiciona logo após a linha `bucket: "nsx"`.

Use `sudo` pra editar — o arquivo é lido pelo systemd unit. Pode usar:

```bash
sudo sed -i '/bucket: "nsx"/a\  capacity_bucket: "nsx_capacity"' /home/nsx_collector/configs/config.yaml
```

(Cuidado pra não duplicar se já existir — confere antes.)

### 4. Restart e verificação imediata

```bash
sudo systemctl restart nsx-collector
sleep 5
sudo journalctl -u nsx-collector --since "30 sec ago" | tail -30
```

Espera ~5 minutos (capacity é slow-path no coletor) e roda:

```bash
sudo journalctl -u nsx-collector --since "6 min ago" | grep -iE 'capacity|error' | tail -20
```

Não deve aparecer `capacity write failed` ou `bucket ... not found`.

### 5. Confirmação no Influx

```bash
TOKEN='FLKJPw-nIgGobRHwhGH2KGVRaoYRvWiMqBuzLqZa8I_La1q2K7Nz_ruSvX1m0wMSW0eFlFo1KpMYer1T6NAz7A=='
URL='http://10.114.35.75:8086/api/v2/query?org=TOTVS'

# capacity de TESP7 no novo bucket
curl -sS -XPOST "$URL" -H "Authorization: Token $TOKEN" \
  -H 'Accept: application/csv' -H 'Content-Type: application/vnd.flux' \
  --data 'from(bucket:"nsx_capacity") |> range(start:-10m) |> filter(fn:(r)=>r._measurement=="nsx_capacity") |> filter(fn:(r)=>r.site=="TESP7") |> last() |> keep(columns:["_time","_field","usage_type","_value"])'
```

Tem que devolver as ~10-15 linhas dos `usage_type` (NUMBER_OF_DFW_SECTIONS,
NUMBER_OF_LOGICAL_SWITCHES, etc.) com timestamps recentes.

### 6. Reportar

Quando terminar, escreva um resumo do que você encontrou e fez:
- Estado do config antes (tinha capacity_bucket? qual valor?)
- Erros nos logs antes
- O que mudou
- Confirmação que capacity_bucket="nsx_capacity" está escrito agora
- Output da query do passo 5 (resumido — só o número de linhas e um exemplo)

## Observações

- **Não** mexa no binário se ele já for >= commit `fc4a39c` (basta o config).
- **Não** crie o bucket `nsx_capacity` no Influx — já foi criado em 2026-05-07.
- O bucket principal `nsx` continua sendo usado pra todo o resto (cluster,
  edge_resource, edge_uplink, alarms, lb_*, logical_router, transport_node,
  manager).
- Se a maioria dos `usage_type` de TESP7 já estavam no bucket `nsx`
  (medido em 2026-05-07 ~15h), eles vão expirar/deslocar — ou ficam só
  como histórico velho. O dashboard será atualizado em separado pra ler
  do bucket novo.
