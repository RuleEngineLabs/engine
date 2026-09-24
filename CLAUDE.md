# RuleEngineLabs — contexto do projeto

## Visão geral
- Motor de regras em Go, sucessor do motor MVEL atual (limitação de performance: reflection).
- Meta de performance: latência quase zero — compilação prévia, sem reflection no caminho de execução.
- Requisitos: condicionais, loops e paralelismo. Paralelismo fica no host (goroutines/errgroup), nunca na linguagem de expressão.
- Engine de expressão adotado: expr-lang/expr.
- Volume considerado: 500 a 1000+ políticas, múltiplas versões cada.
- Dois fluxos centrais: execução da política como máquina de estados, e resgate/cache do artefato compilado (LRU por bytes, singleflight, ponteiro atômico pra blue-green).

## Repositórios (org RuleEngineLabs)
- `RuleEngineLabs/engine` — monorepo Go. Module path: `github.com/RuleEngineLabs/engine`.
- `RuleEngineLabs/console` — frontend Angular 19 novo e separado (gestão/CRUD + preview). Não é o projeto Angular antigo, que segue mirando o motor MVEL.
- Licença MIT gerada como proposta nos dois repos — pergunta se os repos ficam públicos ou proprietários nunca foi respondida.

## Arquitetura (repo engine)
- Um repo, um `go.mod`, dois binários:
  - `cmd/engine` — hot path: rotas `/execute` e `/preview`.
  - `cmd/admin` — CRUD de política, versionamento, dispara invalidação. Importa só `internal/compiler` (não `executor`) — preview roda dentro do `engine`, já que carrega a política inteira no payload.
- Pacotes `internal/`:
  - `policy` — contrato (schema abaixo).
  - `compiler` — policy → artefato (parsing, indexação, validação de grafo, guard de campo obrigatório).
  - `executor` — artefato + entrada → saída (state machine, errgroup, step budget, retry, trace).
  - `cache` — artefato compilado: LRU por bytes, singleflight, ponteiro atômico, L1/L2.
  - `connectors` — pools de conexão HTTP/SQL/NoSQL, cache de resultado (TTL), retry.
- `testdata/` — fixtures da planilha de teste de mesa (cenários de apiCall, retry, validação de compile-time, parallel) como testes table-driven.
- Dois contratos de API: preview (política inteira no payload + entrada) e execução (id no path, entrada no payload).

## Schema de política

### Estrutura geral
- Grafo de states: `id` único, `kind`, `transitions` (array `{when, to}`, avaliado em ordem, primeiro `when` verdadeiro vence), `fallback` (obrigatório em todo state que não for `response`).
- Convenção camelCase em todos os campos (`contextKey`, `timeoutMs`, `ttlSeconds`, `dbQuery`, `apiCall`) — exceção: nome de operação nativa do provider (`getItem`, `hget`, `mget`) mantém o nome real.
- `contextKey` grava a saída do `map` na raiz do contexto (irmão de `input`); `"input"` é reservado, compiler rejeita.
- Dentro de um state referenciado por `each` (parallel), contexto tem `item` + `result`; esse state não pode ter `transitions`/`fallback`/`contextKey`.

### kind: response (terminal)
- Rejeita `transitions`/`fallback`/`contextKey`.
- `status` (literal) + `data` (raiz do corpo, shape livre, aceita expr).

### kind: dbQuery (fase 3)
- `engine`: `sql` (dialect postgres/mysql/sqlserver) ou `nosql` (provider dynamodb/redis primeiro; mongo/cassandra depois).
- `operation` restrita a leitura por provider (dynamodb: `get_item`/`query`, sem `scan`; redis: `get`/`hget`/`mget`).
- `params` só aceita path pro contexto, nunca interpolação de string.

### kind: apiCall (fase 2 = rest; grpc/soap fase 3+)
- REST: `method` + `path` + `body`/`query`.
- gRPC: mensagem dinâmica via protoreflect/dynamicpb, registrado por `connection` no compile, sem codegen por integração.
- `connection` é sempre referência resolvida no servidor, nunca secret/DSN/endpoint cru no JSON (preview devolve a política inteira no payload).
- `kind` não suportado na fase atual é rejeitado por padrão pelo dispatch do compiler.

### Roteamento por resultado (apiCall)
Ordem das transitions:
1. `result.timeout == true` → `providerTimeout`
2. `result.error != null` → `providerUnavailable`
3. `result.status == 400` → `invalidRequest`
4. `result.status >= 500` → `providerUnavailable`
5. `result.status >= 300` → `invalidRequest`

**Em aberto, nunca confirmado:** mover 401/403/408 pra `providerUnavailable` em vez de cair no bucket genérico de `invalidRequest`.

### Retry (apiCall/dbQuery)
- Campo por state, sem herança de `connection`; state sem `retry` = sem retry.
- `maxAttempts` (inteiro, teto proposto ~5, **nunca fixado formalmente**) + `backoffMs` (array de gaps entre tentativas).
- `backoffMs` mais curto que o necessário reusa o último valor.
- `maxAttempts` ausente com `backoffMs` presente: default = `len(backoffMs)+1`.
- `retryable` padrão proposto (não formalizado como lista fechada): `429, 500, 502, 503, 504`.
- Retry + cache: assume que só o resultado final pós-retry é cacheado — **nunca confirmado explicitamente**.

### Cache de resultado (dentro de connectors, distinto do cache de artefato)
- Opcional por state de I/O. Elegibilidade: REST só `method: GET`; SQL heurística de prefixo `SELECT`/`WITH`; NoSQL já restrito por `operation`.
- `ttlSeconds`, teto de 1800 (30 min) no compiler.
- TTL comparado no momento da leitura, usando o `ttlSeconds` do state que lê, não do que gravou.
- Chave de cache é a assinatura resolvida da chamada (`connection`+params/body), não `policyId:versão:stateId` — compartilhado entre políticas que batem no mesmo dado real.

### Guard de campo obrigatório (contextKey)
- compiler cruza `contextKey.campo` referenciado em expr contra a união de campos que algum `map` no grafo escreve nesse `contextKey`; rejeita no publish/preview se o campo nunca aparecer.
- Em runtime, executor checa presença da **chave** (não nulidade do valor) antes de rodar a expressão; ausente aborta com erro genérico antes de chamar `expr.Run`.
- Chave presente com valor `null` passa normalmente (comparação segura, sem erro).

### Envelope de resposta
- Sucesso/erro de política: `{ state, data }` — `data` na raiz, shape livre, status HTTP como literal no state.
- Erro geral do executor (não veio de nenhum state): `{ error, message }`, sem campo `state` — assim se distingue de um response autoral.
- `version` (pra comparação de tráfego espelhado blue-green) vai em header de resposta e em trace/log, nunca dentro de `data`.

### Trace
- `preview` sempre roda com trace ligado; `engine` (`/execute`) não, por custo de alocação por state.
- Trace de state com retry vira array de `attempts` (status + duration por tentativa).
- Trace de `parallel` agrupa falhas por assinatura de `result` (status), listando todos os items que falharam por grupo, sem cap de quantidade.

### kind: parallel
- Campos: `over` (path pro array), `each` (id do state rodado por item), `maxConcurrency` (obrigatório, inteiro 1 a 100, compiler rejeita fora disso, ausência ou não-inteiro).
- `over` resolvendo pra `null` (valor inteiro) OU contendo qualquer elemento `null`: erro runtime `invalid_parallel_source`.
- `over` apontando pra contextKey/campo nunca populado: cai no guard de campo obrigatório (500 geral), não em `invalid_parallel_source`.
- Array vazio é caminho normal (zero iterações).
- Mais itens que `maxConcurrency`: fila/semáforo — no máximo `maxConcurrency` simultâneas por vez, resto aguarda.

## Status / próximos passos
- CI/CD ainda não desenhado — em aberto se o primeiro pipeline publica o JSON Schema do contrato pro `console` ou é build+test do `engine`.
- Próximo passo: esqueleto inicial (`cmd/engine`, `cmd/admin`, `internal/*`) no engine, `ng new` no console.
- Existe planilha de teste de mesa cobrindo o design do apiCall/dbQuery/parallel (cenários, retry, validação de compile-time).
