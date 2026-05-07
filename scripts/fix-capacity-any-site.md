# Padronizar capacity_bucket no nsx-collector — qualquer site

Aplica em **qualquer VM coletora NSX** (TECE, TESP2, TESP3, TESP4, TESP5,
TESP6, TESP7) pra alinhar com o padrão definido em 2026-05-07:

- Capacity grava no bucket dedicado `nsx_capacity` (já existe no Influx
  `10.114.35.75:8086`).
- Demais measurements continuam em `nsx`.
- Dashboard `NSX — Overview` lê capacity de `nsx_capacity`, então qualquer
  site fora do padrão fica sem dados nos painéis de capacity.

A VM coletora geralmente é a própria NSX Manager local OU uma VM
dedicada (`monitoring-redes-1-tespNN` ou `dev-redes`).

## Passos

### 1. Diagnóstico

```bash
cat /home/nsx_collector/configs/config.yaml | grep -A2 influxdb
ls -la /home/nsx_collector/nsx-collector
sudo journalctl -u nsx-collector --since "10 min ago" | grep -iE 'capacity|error|warn|starting' | tail -20
```

Reporta o conteúdo do bloco `influxdb`, mtime do binário, e qualquer linha de
erro/warn relevante.

### 2. Garantir binário atualizado

A última master deve ser commit `fc4a39c` ou maior (fix do uptime do Manager
+ alinhamento do capacity_bucket nos deploy scripts).

```bash
GO=$(command -v go || ls /usr/local/go/bin/go /usr/bin/go 2>/dev/null | head -1)
echo "Go: $GO"

cd /tmp && rm -rf nsx-collector-src && \
git clone -b master git@github.com:totvs-cloud/nsx_collector.git nsx-collector-src && \
cd nsx-collector-src && \
git log -1 --oneline && \
$GO build -o /home/nsx_collector/nsx-collector ./cmd/

ls -la /home/nsx_collector/nsx-collector
```

Se Go não existir, instala (RHEL/Rocky/Alma):

```bash
curl -sSL "https://go.dev/dl/go1.23.6.linux-amd64.tar.gz" -o /tmp/go.tar.gz && \
rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tar.gz && rm /tmp/go.tar.gz
export PATH="/usr/local/go/bin:$PATH"
```

E refaz o build.

### 3. Adicionar/corrigir capacity_bucket no config.yaml

```bash
# adiciona logo após a linha "bucket: ...", se ainda não tiver
grep -q 'capacity_bucket' /home/nsx_collector/configs/config.yaml || \
  sudo sed -i '/^  bucket:/a\  capacity_bucket: "nsx_capacity"' /home/nsx_collector/configs/config.yaml

# valida
grep -E '^  (bucket|capacity_bucket):' /home/nsx_collector/configs/config.yaml
```

A saída deve mostrar **exatamente**:

```
  bucket: "nsx"
  capacity_bucket: "nsx_capacity"
```

Se já existir com outro valor (ex.: `nsx-capacity` com hífen, `nsx_cap`,
etc.), corrige pra `nsx_capacity` exato.

### 4. Restart e verificação dos logs

```bash
sudo systemctl restart nsx-collector
sleep 5
sudo journalctl -u nsx-collector --since "30 sec ago" | grep -E 'starting|capacity_bucket|capacity write failed' | head
```

Deve aparecer:
- `nsx-collector starting ... "capacity_bucket":"nsx_capacity"`
- **Não** deve aparecer `capacity write failed`

Espera ~5 min e confirma que pelo menos uma coleta com `capacity:N` (N>0)
saiu nos logs:

```bash
sudo journalctl -u nsx-collector --since "6 min ago" | grep '"capacity":' | tail -3
```

### 5. Verificar no Influx

Descobre o `<SITE>` desta VM (TESP2, TESP3, TESP5, etc):

```bash
SITE=$(grep -oP 'site:\s*"\K[^"]+' /home/nsx_collector/configs/managers.yaml | head -1)
echo "site=$SITE"
```

Roda a query (token e URL fixos):

```bash
TOKEN='FLKJPw-nIgGobRHwhGH2KGVRaoYRvWiMqBuzLqZa8I_La1q2K7Nz_ruSvX1m0wMSW0eFlFo1KpMYer1T6NAz7A=='
URL='http://10.114.35.75:8086/api/v2/query?org=TOTVS'

# capacity no bucket novo
curl -sS -XPOST "$URL" -H "Authorization: Token $TOKEN" \
  -H 'Accept: application/csv' -H 'Content-Type: application/vnd.flux' \
  --data "from(bucket:\"nsx_capacity\")
    |> range(start:-15m)
    |> filter(fn:(r)=>r._measurement==\"nsx_capacity\")
    |> filter(fn:(r)=>r.site==\"$SITE\")
    |> filter(fn:(r)=>r._field==\"current_usage\")
    |> last()
    |> keep(columns:[\"_time\",\"usage_type\",\"_value\"])"

# uptime de cada Manager (precisa do binário com fix)
curl -sS -XPOST "$URL" -H "Authorization: Token $TOKEN" \
  -H 'Accept: application/csv' -H 'Content-Type: application/vnd.flux' \
  --data "from(bucket:\"nsx\")
    |> range(start:-2m)
    |> filter(fn:(r)=>r._measurement==\"nsx_manager\")
    |> filter(fn:(r)=>r.site==\"$SITE\")
    |> filter(fn:(r)=>r._field==\"uptime_ms\")
    |> last()
    |> keep(columns:[\"site\",\"manager_ip\",\"_value\"])"
```

Esperado:
- 1ª query: ~25-30 linhas com `usage_type` (DFW_SECTIONS, LOGICAL_PORTS, etc.)
- 2ª query: 3 linhas (uma por Manager) com `_value` em milissegundos > 0

### 6. Reportar

Resumo curto:
- Site/VM/IP
- Estado do config antes (tinha capacity_bucket?)
- Commit do binário compilado
- Output do passo 5 (resumido — número de linhas e exemplo de valor)
- Anomalias se houver

## Observações

- **Não** crie o bucket `nsx_capacity` no Influx — já existe.
- Os deploy scripts (`deploy-*.sh`) já estão atualizados pra incluir
  `capacity_bucket` automaticamente — execuções futuras de deploy não
  precisam dessa correção manual.
- Capacity antigo no bucket `nsx` (de antes da migração) vai expirar
  naturalmente — não precisa apagar.
