# RuleEngineLabs — convenções de CI/CD (compartilhado)

Cópia idêntica nos dois repos: `RuleEngineLabs/engine` e `RuleEngineLabs/console`. Mudou aqui, atualiza nos dois. Nada específico de Go entra neste arquivo — a convenção de import versioning (SIV) fica só no `CLAUDE.md` do `engine`.

## Branches

- `feature/**` → push: roda CI da branch (build/test/lint). PR pra `develop` (head = branch, base = `develop`): se já existe PR aberto com esse head, não cria outro — push nele já atualiza o diff sozinho, só precisa garantir que CI dispare no evento `synchronize`. Se não existe, cria.
- Recomendado: "delete branch on merge" ligado no repo, pra não reabrir PR sem querer reusando nome de branch já mergeada.
- `develop` → push: roda CI da própria `develop`. Commit **não** entra automaticamente em nenhuma release já cortada — não existe passo de sincronizar `develop` → `release/*`. Se uma correção precisa entrar numa release em andamento, é ação explícita: PR pequeno com cherry-pick, base direto em `release/X.Y.Z`.
- Corte de release: trigger manual (`workflow_dispatch`), nunca automático a cada push em `develop`. Cria `release/X.Y.Z` a partir do HEAD de `develop` naquele momento (branch nova, não PR). Só uma release em estabilização por vez.
- Estabilização: bugs viram `fix/**` com PR pra `release/X.Y.Z`, mesma lógica de criar/atualizar do passo de `feature/**`.
- `release/X.Y.Z` → `main`: fora de escopo deste documento (já resolvido por fora).
- `release/X.Y.Z` → `develop`: merge de volta obrigatório depois do merge em `main`, pra correções feitas só na release não sumirem na próxima. Pode disparar automático assim que o merge em `main` fecha.

## Versionamento (Conventional Commits, SemVer)

- Bump computado a partir dos commits em `develop` desde a última tag de release — nenhum humano escolhe o número na hora do corte.
- `feat!:` ou rodapé `BREAKING CHANGE:` → MAJOR. `feat:` (sem breaking) → MINOR. `fix:` (sem feat) → PATCH. Só `chore:`/`docs:`/`ci:`/`refactor:` etc. → nenhum bump, não corta release.
- Usa o bump mais alto entre todos os commits do intervalo.
- `engine` e `console` versionam separado — cada repo varre só o próprio histórico desde a própria última tag.
- Requisito: lint de commit message (commitlint ou equivalente) obrigatório na PR `feature/**` → `develop`, senão a varredura não tem o que ler.
