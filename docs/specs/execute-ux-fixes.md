# Spec: UX Fixes for POST /execute/{id}

**Status:** draft  
**Date:** 2026-10-04  
**Author:** agent.analista  
**Endpoint:** `POST /execute/{id}` (`cmd/admin/execute.go`)

---

## Contexto

O handler `handleExecute` apresenta cinco problemas de UX que afetam consistência de resposta, segurança de informação, observabilidade do lado do cliente e conformidade com HTTP. Esta spec descreve o comportamento esperado e os critérios de aceitação para cada um dos cinco fixes, sem quebrar o contrato de resposta existente.

**Contrato existente (imutável):** os campos `state`, `data` e `trace` da `executor.Result` devem permanecer presentes e com a mesma semântica nos casos de sucesso.

---

## Fix 1 — 404 bypassa `writeError`

### Problema

Quando `ps.Current(id)` retorna erro, o handler escreve o cabeçalho e o corpo manualmente em vez de usar o helper `writeError`. O corpo inline usa `map[string]string{"error": "policy_not_found"}` enquanto todos os outros erros usam `errorResponse{Error: msg}`. Essa divergência no caminho de serialização quebra qualquer parser de erros genérico e dificulta testes.

**Trecho atual (linhas 62–66):**
```go
w.Header().Set("Content-Type", "application/json")
w.WriteHeader(http.StatusNotFound)
json.NewEncoder(w).Encode(map[string]string{"error": "policy_not_found"})
```

### Comportamento esperado

O path de 404 deve delegar para `writeError(w, http.StatusNotFound, "policy_not_found")`, produzindo o mesmo JSON que todos os outros erros do handler.

### Critérios de aceitação

**CA-1.1**
```
Given  uma requisição POST /execute/{id} com um {id} que não existe no PolicyStore
When   o handler processa a requisição
Then   o status HTTP é 404
And    o Content-Type é "application/json"
And    o corpo é exatamente {"error":"policy_not_found"}
```

**CA-1.2 — Consistência de estrutura**
```
Given  qualquer resposta de erro do endpoint (400, 403, 404, 429, 500)
When   o corpo JSON é desserializado
Then   o objeto tem exatamente um campo de nível superior: "error" (string)
And    não há campos adicionais no objeto raiz
```

---

## Fix 2 — Erro interno exposto ao cliente

### Problema

Quando `executor.Execute` retorna erro, o handler passa `err.Error()` diretamente ao cliente:

```go
writeError(w, http.StatusInternalServerError, err.Error())
```

Isso pode vazar detalhes internos de implementação (ex.: `"state \"eval\" not found during execution"`) para o consumidor, criando superfície para engenharia reversa da lógica interna. O erro já é logado via `slog.Error` com o campo `"err"`, portanto a informação não se perde para operações.

### Comportamento esperado

A mensagem enviada ao cliente deve ser a string literal `"internal execution error"`, independente do conteúdo de `err`. O log de erro existente (`log.Error("execute failed", ...)`) deve ser mantido intacto com o `err` real.

### Critérios de aceitação

**CA-2.1 — Corpo opaco**
```
Given  uma policy que provoca erro interno em executor.Execute (ex.: state inválido)
When   a requisição é processada
Then   o status HTTP é 500
And    o corpo é exatamente {"error":"internal execution error"}
And    o corpo não contém nenhuma mensagem de stack trace, nome de estado ou detalhe interno
```

**CA-2.2 — Log preservado**
```
Given  o mesmo cenário de erro interno
When   a requisição é processada
Then   o log de nível ERROR contém o campo "err" com o valor original do erro
And    o log contém o campo "policy" com o nome da policy
```

---

## Fix 3 — HTTP 429 sem `Retry-After`

### Problema

Quando o rate limiter rejeita uma requisição `noCache`, o handler retorna 429 sem o header `Retry-After`. Consumidores que respeitam a RFC 6585 não têm como saber quando tentar novamente, o que tipicamente resulta em retry storms com backoff heurístico ou abandono.

O limiter é instanciado com janela de 1 segundo (`ratelimit.New(time.Second, 10)` em `main.go`). Portanto, o valor correto do header é `"1"`.

**Trecho atual:**
```go
writeError(w, http.StatusTooManyRequests, "rate limit exceeded for noCache")
```

### Comportamento esperado

Antes de chamar `writeError`, o handler deve setar `w.Header().Set("Retry-After", "1")`. O header deve ser visível na resposta ao cliente independente de o cliente ter enviado `X-Request-ID` ou não.

### Critérios de aceitação

**CA-3.1**
```
Given  um cliente com noCache=true que excede o limite de 10 chamadas por segundo
When   a 11ª chamada é processada
Then   o status HTTP é 429
And    o header "Retry-After" está presente com o valor "1"
And    o corpo é {"error":"rate limit exceeded for noCache"}
```

**CA-3.2 — Chamadas dentro do limite**
```
Given  um cliente com noCache=true que faz exatamente 10 chamadas em 1 segundo
When   cada chamada é processada
Then   nenhuma delas retorna 429
And    nenhuma delas inclui o header "Retry-After"
```

---

## Fix 4 — Sem correlation ID na response

### Problema

Nenhum request ID é gerado nem propagado. Quando um cliente recebe um 500, não há como correlacionar a resposta com a entrada de log correspondente no servidor. Isso aumenta significativamente o MTTR em incidentes de produção.

### Comportamento esperado

Todas as respostas do endpoint — tanto erros quanto sucesso — devem incluir o header `X-Request-ID`.

Comportamento de forwarding/geração:
- Se a requisição contém o header `X-Request-ID`, seu valor é ecoado na resposta.
- Se a requisição não contém `X-Request-ID`, o servidor gera um ID único (UUID v4 ou equivalente criptograficamente seguro) e o inclui na resposta.

O `X-Request-ID` deve ser atribuído antes de qualquer `writeError` no handler, portanto deve ser setado no início do processamento (ou via middleware que injeta no contexto). O valor gerado ou recebido deve ser incluído nas entradas de log relevantes (`"request_id"` field).

### Critérios de aceitação

**CA-4.1 — Forward de ID existente**
```
Given  uma requisição com header "X-Request-ID: abc-123"
When   a requisição é processada (qualquer outcome: 200, 400, 404, 429, 500)
Then   a resposta contém o header "X-Request-ID: abc-123"
```

**CA-4.2 — Geração quando ausente**
```
Given  uma requisição sem header "X-Request-ID"
When   a requisição é processada (qualquer outcome)
Then   a resposta contém o header "X-Request-ID" com um valor não vazio
And    o valor tem formato de UUID v4 (xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx)
```

**CA-4.3 — Presença em todos os status codes**
```
Given  cenários que produzem 200, 400, 403, 404, 429 e 500
When   cada requisição é processada
Then   todas as respostas contêm o header "X-Request-ID"
```

**CA-4.4 — Propagação ao log**
```
Given  qualquer requisição processada pelo handler
When   a requisição é processada
Then   as entradas de log emitidas pelo handler (ok e error) contêm o campo "request_id"
And    o valor do campo é idêntico ao header "X-Request-ID" da resposta
```

---

## Fix 5 — `duration_ms` logado mas não entregue ao consumidor

### Problema

O handler já calcula `durationMs` e o inclui no log:

```go
log.Info("execute ok", "policy", rec.Name, "state", result.State, "duration_ms", durationMs)
```

Mas a resposta ao cliente encoda apenas `result` diretamente, sem `duration_ms`:

```go
json.NewEncoder(w).Encode(result)
```

Integradores que precisam monitorar latência de execução no lado deles são forçados a instrumentar proxies externos. O dado já está disponível; basta entregá-lo.

### Comportamento esperado

A resposta de sucesso deve incluir o campo `duration_ms` (int64, em milissegundos) no objeto JSON raiz. Os campos existentes `state`, `data` e `trace` devem permanecer inalterados.

Estratégia preferida: criar um envelope de resposta local no handler (sem alterar `executor.Result`) que embedda os campos de `Result` e acrescenta `duration_ms`. Isso mantém `executor.Result` independente de preocupações HTTP.

### Critérios de aceitação

**CA-5.1 — Campo presente**
```
Given  uma policy válida que executa com sucesso
When   POST /execute/{id} é chamado
Then   o status HTTP é 200
And    o corpo JSON contém o campo "duration_ms" (número inteiro >= 0)
And    o campo "state" está presente e correto
And    o campo "data" está presente e correto
And    o campo "trace" está presente quando não vazio (comportamento omitempty mantido)
```

**CA-5.2 — Valor plausível**
```
Given  uma policy que leva entre 0 ms e 5000 ms para executar
When   a resposta é recebida
Then   o valor de "duration_ms" é um inteiro no intervalo [0, 5000]
```

**CA-5.3 — Não presente em respostas de erro**
```
Given  uma requisição que resulta em 400, 403, 404, 429 ou 500
When   a resposta é recebida
Then   o corpo JSON não contém o campo "duration_ms"
```

**CA-5.4 — Contrato backward-compatible**
```
Given  um cliente que consome apenas "state", "data" e "trace" (ignora campos adicionais)
When   a resposta com "duration_ms" é desserializada
Then   o cliente continua funcionando corretamente sem nenhuma mudança de código
```

---

## Contrato de response atualizado

### Sucesso (HTTP 200)

```json
{
  "state": "approved",
  "data": { "...": "..." },
  "trace": [
    { "stateId": "start",    "duration_ms": 2 },
    { "stateId": "evaluate", "duration_ms": 5 },
    { "stateId": "approved", "duration_ms": 1 }
  ],
  "duration_ms": 8
}
```

Notas:
- `trace` permanece `omitempty` — ausente quando a policy não gera trace.
- `duration_ms` no nível raiz é o tempo total de `executor.Execute`, em ms.
- `duration_ms` dentro de cada `TraceEntry` é tempo por estado (já existia, não muda).

### Erro (qualquer status 4xx/5xx)

```json
{
  "error": "mensagem de erro"
}
```

Estrutura sem alterações. Nenhum campo `duration_ms` em respostas de erro.

### Headers obrigatórios em todas as respostas

| Header            | Requisito                                                     |
|-------------------|---------------------------------------------------------------|
| `Content-Type`    | `application/json` (já presente — manter)                     |
| `X-Request-ID`    | Valor da requisição se presente; UUID v4 gerado caso contrário |
| `Retry-After`     | `"1"` apenas em respostas 429 de rate limit                   |

---

## Restrições e não-objetivos

1. **Contrato backward-compatible:** `state`, `data` e `trace` não mudam de semântica, tipo nem posição. `duration_ms` é additive.
2. **`executor.Result` permanece limpo:** a struct do pacote `executor` não deve receber campos HTTP. O envelope de resposta é responsabilidade do handler.
3. **Sem breaking change em erros:** a estrutura `{"error": "..."}` permanece. Nenhum campo novo é adicionado a respostas de erro.
4. **`X-Request-ID` é opcional na entrada:** o servidor aceita requests sem esse header e gera um. Não rejeita requests que o omitem.
5. **`Retry-After` somente em 429 de rate limit:** não adicionar em outros 429 hipotéticos nem em outros status codes.
6. **Shadow execution:** os fixes não alteram o comportamento do shadow goroutine (`runShadow`). O `duration_ms` da resposta mede apenas o tempo da execução estável, não do shadow.
7. **Cobertura:** cada fix deve ter testes unitários cobrindo os critérios de aceitação acima. A cobertura geral do pacote `cmd/admin` deve permanecer >= 97% (DoD do projeto).
