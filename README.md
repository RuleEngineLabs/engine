# engine

Motor de regras em Go: compila políticas (grafo de estados) e executa com latência mínima.

## Visão geral

- `cmd/engine` — hot path: rotas `/execute/{id}` e `/preview`
- `cmd/admin` — CRUD de políticas e versionamento
- `internal/policy` — tipos e contrato do domínio
- `internal/compiler` — policy → artefato compilado
- `internal/executor` — artefato + entrada → saída (state machine)
- `internal/cache` — LRU por bytes, singleflight, blue-green

## Requisitos

- Go 1.24+
- Docker (para integração)

## Execução local

### 1. Build e start

```bash
# Build
go build -o bin/admin ./cmd/admin

# Start (porta padrão :8081)
./bin/admin

# Start em porta alternativa
ADDR=:9090 ./bin/admin
```

### 2. Verificar saúde

```bash
curl http://localhost:9090/health
# → {"status":"ok"}
```

### 3. Criar e executar uma policy

```bash
# Criar
curl -X POST http://localhost:9090/policies \
  -H "Content-Type: application/json" \
  -H "X-Owner-Group: team-a" \
  -d '{
    "name": "exemploPolicy",
    "entry": "inicio",
    "states": [
      { "id": "inicio", "kind": "response", "data": { "msg": "ok" }, "status": 200 }
    ]
  }'
# → {"policyId":"<ID>","version":1}

# Executar (substituir <ID> pelo policyId retornado acima)
curl -X POST http://localhost:9090/execute/<ID> \
  -H "Content-Type: application/json" \
  -d '{ "input": "qualquer" }'
# → {"state":"inicio","data":{"msg":"ok"}}
```

### 4. Preview (simula sem efeitos colaterais)

```bash
curl -X POST http://localhost:9090/preview \
  -H "Content-Type: application/json" \
  -d '{
    "name": "teste",
    "entry": "gravar",
    "states": [
      {
        "id": "gravar",
        "kind": "apiCall",
        "method": "POST",
        "url": "http://api.externa/pedidos",
        "contextKey": "resultado",
        "transitions": [{ "when": "true", "to": "fim" }]
      },
      { "id": "fim", "kind": "response", "data": "ok", "status": 200 }
    ],
    "input": {}
  }'
# → trace com blocked:true no state de escrita — sem chamada real
```

### 5. Testes

```bash
# Todos os testes
go test ./...

# Com cobertura (DoD: ≥97% em store + admin)
go test -coverprofile=cov.out \
  -coverpkg=./internal/store/,./cmd/admin/ \
  ./cmd/admin/
go tool cover -func=cov.out | tail -1
```

### 6. Docker

```bash
docker build -t engine:local .
docker run -p 9090:8081 engine:local
```

### Variáveis de ambiente

| Variável | Padrão | Descrição |
|---|---|---|
| `ADDR` | `:8081` | Endereço de escuta do servidor |
| `ENVIRONMENT` | — | `staging` habilita `origin=benchmark` no noCache |

## Observabilidade

### Formato dos logs

O servidor emite logs estruturados em JSON (newline-delimited) no stdout:

```json
{"time":"2026-09-29T19:00:00Z","level":"INFO","msg":"request","request_id":"a1b2c3d4-e5f6-7890-ab12-cdef01234567","method":"POST","path":"/execute/abc","status":200,"duration_ms":3}
{"time":"2026-09-29T19:00:00Z","level":"INFO","msg":"execute ok","request_id":"a1b2c3d4-e5f6-7890-ab12-cdef01234567","policy":"exemploPolicy","state":"inicio","duration_ms":2}
```

Campos presentes em toda linha de request:

| Campo | Tipo | Descrição |
|---|---|---|
| `request_id` | string | ID de correlação — propagado em todas as linhas do mesmo request |
| `method` | string | Método HTTP |
| `path` | string | Caminho da requisição |
| `status` | int | Código HTTP retornado |
| `duration_ms` | int | Tempo total de processamento em ms |

Nível por faixa de status: `INFO` (2xx/3xx) · `WARN` (4xx) · `ERROR` (5xx).

### Correlation ID

Cada requisição recebe um `X-Request-ID` único. O cliente pode fornecer o próprio:

```bash
curl -X POST http://localhost:9090/execute/<ID> \
  -H "X-Request-ID: meu-trace-123" \
  -H "Content-Type: application/json" \
  -d '{}'
# Response header: X-Request-ID: meu-trace-123
# Todos os logs deste request terão: "request_id":"meu-trace-123"
```

Se o header não for enviado, o servidor gera um ID aleatório automaticamente.

### Filtrando logs por request

```bash
# Todos os logs de um request específico
./bin/admin | grep '"request_id":"meu-trace-123"'

# Apenas erros
./bin/admin | grep '"level":"ERROR"'

# Com jq
./bin/admin | jq 'select(.level == "ERROR") | {request_id, path, status, duration_ms}'
```

## Desenvolvimento

```bash
go test ./...
go build ./cmd/engine
go build ./cmd/admin
```

## Identidade do agente

Operações automatizadas (PRs, merges feature→develop) são realizadas por `ruleenginelabs-agent[bot]` (GitHub App ID 5063188).  
`@LucasLimaLL` aparece apenas no CODEOWNERS como revisor humano de releases (develop→main).

O token de instalação é gerado via RS256 JWT, renovado automaticamente no início de cada sessão pelo hook `SessionStart` (`ensure-agent-token.ps1`), e injetado via `~/.bashrc` antes de qualquer chamada `gh`.

## CI/CD

| Branch | Trigger | Ação |
|---|---|---|
| `feature/*` | push | go vet + test + coverage → abre PR para `develop` |
| `develop` | push/merge | go vet + test → cria `release/vX.Y.Z` + PR para `main` |
| `release/*` | push | go vet + test + Docker build → valida artefato |
| `main` | PR merge | produção (deploy não configurado ainda) |
