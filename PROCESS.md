# Fluxo de Trabalho — RuleEngineLabs Engine

Este documento descreve o processo que a equipe segue desde a definição de uma história até ela estar em `main`. O objetivo é reduzir ciclos de feedback sem criar reuniões — cada papel sabe o que fazer sem precisar perguntar.

---

## O Kanban

```
Backlog → Ready → In Progress → In Review → Done
```

**Backlog** — história definida mas ainda não verificada para início.
**Ready** — DoR completo, dev pode pegar.
**In Progress** — dev trabalha, branch aberta.
**In Review** — PR aberto, aguardando revisão e QA.
**Done** — merged em `develop` (segue para `release → main` pelo fluxo de release existente).

**WIP limit:** máximo **2 histórias** simultâneas em "In Progress" por dev. Se estiver no limite, desbloqueie revisões antes de pegar nova história.

---

## Critérios de entrada em cada coluna

### Backlog → Ready
Quem aprova: **Analista** confirma DoR; **Dev** confirma viabilidade técnica.

- [ ] Historia escrita na forma "Como / Quero / Para"
- [ ] Cenários Gherkin definidos e revisados por QA antes da história entrar em dev
- [ ] Dependências de código identificadas (não apenas dependências de integração)
- [ ] Estimativa de tamanho (Size) preenchida no board
- [ ] Sem bloqueadores conhecidos

> **Distinção crítica**: dependência de código (o código X precisa existir) vs. dependência de integração (o sistema Y precisa estar no ar). Dependências de integração podem ser resolvidas com stubs durante o desenvolvimento — isso não bloqueia mover para Ready.

### Ready → In Progress
Quem decide: **Dev** se auto-atribui.

- [ ] Dev não está acima do WIP limit (máx. 2 em progresso)
- [ ] Kickoff de 15 min com Analista e QA antes de começar (alinhar Gherkin, não reunião de status)
- [ ] Branch criada no padrão: `feature/US-XXX-descricao-curta`
- [ ] Issue atualizada com assignee e status "In Progress"

### In Progress → In Review
Quem decide: **Dev** certifica antes de abrir PR.

- [ ] Todos os cenários Gherkin têm testes passando
- [ ] `go test ./...` passa sem regressões
- [ ] DoD checklist auto-preenchido na issue
- [ ] PR aberto com link para a issue (`Closes #XXX`)
- [ ] PR tem descrição de: o que mudou, como testar, rollback se aplicável

### In Review → Done
Quem decide: **QA** libera; **Dev** mergeia.

- [ ] Code review aprovado (mínimo 1 revisor)
- [ ] QA confirmou cada critério de aceite na issue (comentário ou checklist)
- [ ] Sem comentários abertos no PR
- [ ] Squash-merge em `develop`
- [ ] KB atualizado na issue SE comportamento público mudou (campo em aberto → fechado)

---

## O fluxo completo de uma história

```
1. [Analista] Escreve história + Gherkin, checa DoR
2. [Analista + QA] Revisam Gherkin juntos (15 min) → história vai para Ready
3. [Dev] Pega história (auto-assign), faz kickoff com Analista+QA
4. [Dev] Implementa na branch feature/US-XXX
5. [Dev] Certifica DoD, abre PR
6. [Revisor] Code review
7. [QA] Testa contra cada critério Gherkin, comenta na issue
8. [Dev] Aplica feedback, PR aprovado
9. [Dev] Squash-merge em develop → issue fechada → card Done
```

**Onde o retrabalho acontece e como evitar:**

| Ponto de risco | Causa comum | Como evitar |
|---|---|---|
| Dev entrega algo diferente do esperado | Analista e QA nunca viram o Gherkin juntos | Passo 2: revisão conjunta obrigatória antes de entrar em dev |
| QA reprova tarde após muito código | QA só envolve no final | Passo 3: kickoff inclui QA — QA sabe o que vai testar antes do dev começar |
| Merge bloqueia outra história | Branch muito antiga, conflitos | WIP limit + merge frequente em develop |
| Regressão não detectada | Testes só cobrem o happy path | DoD exige testes dos cenários Gherkin completos, incluindo cenários de erro |

---

## Quem decide em cada etapa

| Etapa | Papel | Decisão |
|---|---|---|
| Mover para Ready | **Analista** | "Os critérios de aceite estão claros e testáveis?" |
| Pegar história | **Dev** | "Tenho capacidade e o trabalho está desbloqueado?" |
| Abrir PR | **Dev** | "Meus testes cobrem os cenários Gherkin?" |
| Aprovar código | **Qualquer dev** | "O código faz o que diz e está no padrão?" (ver AGENTS.md) |
| Liberar para merge | **QA** | "Cada critério de aceite passa no ambiente de dev?" |
| Merge | **Dev que abriu o PR** | Squash-merge após QA liberar |

---

## Sequência de priorização das histórias

As histórias têm duas trilhas paralelas que podem correr simultaneamente:

**Trilha A — Motor de execução:**
US-001 → US-002 → US-003 → US-004 → US-005 → US-006 → US-007 → US-008 → US-009

**Trilha B — Storage e plataforma:**
US-012 → US-013 → US-014 → US-015 → US-016 → US-017 → US-018 → US-019 → US-020

**Dependem de A + B:**
US-021 → US-022 → US-023 (Canary)
US-024 → US-025 → US-026 (Sombra)

**Dependem de A + B + infra (EPIC-011):**
US-027 → US-028 → US-029 (Sandbox)

**Infra (pode correr em paralelo com B):**
US-030 → US-031

**Console (só após A concluída):**
US-010, US-011

**Regra para começar a próxima história:**
- Dentro de uma trilha: a história anterior precisa estar em `develop` (Done).
- Entre trilhas: pode começar a qualquer momento se o DoR estiver completo.
- Nunca mova para In Progress se houver 2+ histórias em In Review — desbloqueie primeiro.

---

## O que garante que cada história chega ao main

1. A história fecha em `develop` quando QA libera (Done no kanban).
2. `develop` segue para `release` via workflow de release-cut (manual, periódico).
3. `release` segue para `main` via PR com os mesmos gates de CI (validate + docker).
4. O PR de release só fecha quando todos os itens do Done da sprint estão em develop.

---

## Documentando aprendizados conforme o processo evolui

Não espere o processo estar perfeito para registrar. A cada história concluída:
- Se algo no processo criou atrito → anote num comentário na issue com a tag `[PROCESSO]`
- Após cada EPIC completo → capture os aprendizados em `docs/retros/EPIC-XXX.md`
- Este arquivo (PROCESS.md) é vivo — PRs para mudanças nele seguem o mesmo fluxo, mas são rápidos (não precisam de QA formal)

---

## Versão

v1.0 — 2026-09-28. Baseado nas histórias US-001..US-031 e no fluxo de release existente do repositório.
