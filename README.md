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

## Desenvolvimento

```bash
go test ./...
go build ./cmd/engine
go build ./cmd/admin
```

## CI/CD

| Branch | Trigger | Ação |
|---|---|---|
| `feature/*` | push | go vet + test + coverage → abre PR para `develop` |
| `develop` | push/merge | go vet + test → cria `release/vX.Y.Z` + PR para `main` |
| `release/*` | push | go vet + test + Docker build → valida artefato |
| `main` | PR merge | produção (deploy não configurado ainda) |
