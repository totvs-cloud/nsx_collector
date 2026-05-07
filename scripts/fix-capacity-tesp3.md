# Fix nsx-collector — TESP3

Objetivo: deixar o coletor do TESP3 com:
- Binário compilado a partir da master atual (commit `fc4a39c` ou superior, com fix do uptime do Manager)
- Capacity gravando no bucket `nsx_capacity` (que já existe no Influx)
- Service rodando saudável

A VM do coletor do TESP3 é `10.100.29.200`. Você pode rodar este MD direto via
Claude Code na VM, ou executar os comandos manualmente.

## Passos

### 1. Diagnóstico inicial

```bash
cat /home/nsx_collector/configs/config.yaml
ls -la /home/nsx_collector/nsx-collector
sudo journalctl -u nsx-collector --since "10 min ago" | grep -iE 'capacity|error|warn|starting' | tail -30
```

Reporta:
- Tem `capacity_bucket:` no config.yaml? Qual valor?
- Mtime do binário (se for anterior a 2026-05-07 ~16:30 BRT está velho).
- Tem erro `bucket "nsx_capacity" not found` nos logs? Tem outros erros?

### 2. Recompilar o binário

Descobre onde Go está instalado:

```bash
command -v go || ls /usr/local/go/bin/go /usr/bin/go 2>/dev/null
```

Use o path encontrado abaixo (substitua `<GO>` por `go`, `/usr/bin/go` ou
`/usr/local/go/bin/go`):

```bash
cd /tmp && rm -rf nsx-collector-src && \
git clone -b master git@github.com:totvs-cloud/nsx_collector.git nsx-collector-src && \
cd nsx-collector-src && \
<GO> build -o /home/nsx_collector/nsx-collector ./cmd/

# confirma que pegou commit certo
git -C /tmp/nsx-collector-src log -1 --oneline
```

A última master deve ser `fc4a39c` ou maior (fix do uptime aninhado em
`system_status` no `/api/v1/cluster/nodes/<id>/status`).

Se Go não estiver instalado, instala (RHEL/Rocky/Alma):

```bash
curl -sSL "https://go.dev/dl/go1.23.6.linux-amd64.tar.gz" -o /tmp/go.tar.gz && \
rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tar.gz && \
rm /tmp/go.tar.gz && export PATH="/usr/local/go/bin:$PATH"
```

E refaz o build acima usando `/usr/local/go/bin/go`.

### 3. Ajustar o config.yaml

Garante que o bloco `influxdb` tenha `capacity_bucket: "nsx_capacity"`:

```bash
# adiciona logo após "bucket: ..." (não duplica se já existir)
grep -q 'capacity_bucket' /home/nsx_collector/configs/config.yaml || \
  sudo sed -i '/^  bucket:/a\  capacity_bucket: "nsx_capacity"' /home/nsx_collector/configs/config.yaml

# confere
cat /home/nsx_collector/configs/config.yaml
```

Se já existia com outro valor (ex.: `nsx-capacity` com hífen), corrige
manualmente pra `nsx_capacity` (underscore).

### 4. Restart

```bash
sudo systemctl restart nsx-collector
sleep 5
sudo journalctl -u nsx-collector --since "30 sec ago" | grep -E 'starting|capacity_bucket|error' | head
```

A linha `nsx-collector starting` no log deve mostrar
`"capacity_bucket":"nsx_capacity"`.

### 5. Verificar no Influx

Espera ~5 min (capacity é slow-path) e roda:

```bash
TOKEN='FLKJPw-nIgGobRHwhGH2KGVRaoYRvWiMqBuzLqZa8I_La1q2K7Nz_ruSvX1m0wMSW0eFlFo1KpMYer1T6NAz7A=='
URL='http://10.114.35.75:8086/api/v2/query?org=TOTVS'

# capacity de TESP3 no bucket novo
curl -sS -XPOST "$URL" -H "Authorization: Token $TOKEN" \
  -H 'Accept: application/csv' -H 'Content-Type: application/vnd.flux' \
  --data 'from(bucket:"nsx_capacity")
    |> range(start:-15m)
    |> filter(fn:(r)=>r._measurement=="nsx_capacity")
    |> filter(fn:(r)=>r.site=="TESP3")
    |> filter(fn:(r)=>r._field=="current_usage")
    |> last()
    |> keep(columns:["_time","usage_type","_value"])'

# uptime de cada Manager do TESP3 (precisa do binário novo)
curl -sS -XPOST "$URL" -H "Authorization: Token $TOKEN" \
  -H 'Accept: application/csv' -H 'Content-Type: application/vnd.flux' \
  --data 'from(bucket:"nsx")
    |> range(start:-2m)
    |> filter(fn:(r)=>r._measurement=="nsx_manager")
    |> filter(fn:(r)=>r.site=="TESP3")
    |> filter(fn:(r)=>r._field=="uptime_ms")
    |> last()
    |> keep(columns:["site","manager_ip","_value"])'
```

Esperado:
- 1ª query: ~29 linhas com `usage_type` (DFW_SECTIONS, LOGICAL_PORTS, etc.)
- 2ª query: 3 linhas (uma por Manager) com `_value` em milissegundos

### 6. Reportar

Resumo do que mudou e qualquer anomalia (logs com erro, queries vazias, etc).

## Observações

- **Não** crie o bucket `nsx_capacity` no Influx — já foi criado em 2026-05-07.
- O bucket principal `nsx` continua sendo usado pra todo o resto.
- Se já tiver dado de capacity de TESP3 no bucket `nsx` (de antes), eles vão
  expirar/ficar como histórico velho — não é problema.
