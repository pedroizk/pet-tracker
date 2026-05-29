# Modo perdido + link público

## Ideia

Hoje o app rastreia o pet em tempo real, mas se ele realmente fugir o dono não tem como mobilizar a vizinhança rapidamente. O **Modo perdido** resolve isso adicionando um botão na linha de cada pet que muda o estado para "perdido" e gera um **link público read-only** com a última posição e o rastro recente. O link pode ser colado em grupos de WhatsApp do bairro, postado em redes sociais ou enviado para o petshop da esquina — qualquer um que abra vê o mapa, sem login.

O valor é direto: transforma o app de uma ferramenta privada em um **megafone comunitário** no momento em que mais importa. O dono não precisa explicar onde o pet sumiu; basta enviar a URL. Vizinhos veem o mapa atualizando sozinho, e quando o pet for encontrado o dono clica em "Marcar como encontrado" e o link expira automaticamente.

**Cenário típico:** Maria perde a gata Mia às 14h. Abre o app, clica em **🚨 Marcar como perdida** na linha da Mia. O modal mostra o link `http://pettracker/lost/8a3f...` com botão "Copiar" e "Compartilhar no WhatsApp". Maria cola o link no grupo "Vizinhos Centro Pelotas". O Sr. Carlos abre o link às 14h20, vê no mapa que a Mia passou pela praça há 5 minutos, sai pra olhar e a encontra. Maria volta no app e clica em "Marcar como encontrada" — o link `8a3f...` para de funcionar.

## Implementação

**Backend (`main.go`):**

- Adicionar campos ao struct `Pet`:
  ```go
  Lost      bool       `json:"lost"`
  LostToken string     `json:"lost_token,omitempty"`
  LostSince *time.Time `json:"lost_since,omitempty"`
  ```
- Adicionar ao `Registry` um índice secundário `tokenIndex map[string]int64` protegido pelo mesmo `r.mu`.
- Novos métodos no `Registry`:
  - `MarkLost(petID int64) (token string, ok bool)` — gera token via `crypto/rand` (16 bytes hex), grava em `pet.LostToken`, atualiza `tokenIndex`, broadcast `StreamMessage{Type: "lost_status"}`.
  - `ClearLost(petID int64) bool` — limpa flags, remove do `tokenIndex`, broadcast.
  - `LookupByToken(token string) (*PetTracker, bool)` — leitura O(1).
- Novos endpoints (registrar em `main()`):
  - `POST /pets/{id}/lost` → `petItemHandler` ramo `"lost"` chama `handleLost(...)` com `MarkLost`. Retorna `{lost: true, lost_token, public_url}`.
  - `DELETE /pets/{id}/lost` → ramo `"lost"` chama `ClearLost`. Retorna 204.
  - `GET /lost/{token}` → handler `lostPagePublicHandler` que serve `lost.html` (file server filtrado).
  - `GET /lost/{token}/state` → JSON com `{pet, position, history (últimos 100 pontos), lost_since}`.
- Adicionar `Type: "lost_status"` ao `StreamMessage` com campos `Lost bool` e `Token string`.
- Validações: token deve ter exatamente 32 caracteres hex; petID inexistente → 404; tentar marcar como perdido um pet já perdido devolve o token existente (idempotente).

**Frontend (`index.html`):**

- Pet row: adicionar ação `<button data-act="lost">🚨 Marcar como perdido</button>`. Quando `pet.lost` é `true`, troca para `<button data-act="found" class="success">✅ Marcar como encontrado</button>`.
- Quando `pet.lost`, adicionar classe `pet-row.lost` (borda esquerda 3px sólida `#e74c3c`) e badge `<span class="badge lost">PERDIDO</span>`.
- Novo modal (overlay reaproveitando o estilo de `#help-overlay`): mostra link copiável + botão "Copiar" + botão "Compartilhar no WhatsApp" (`https://wa.me/?text=`).
- Função `markLost(petID)` faz `POST /pets/{id}/lost`, abre o modal com a URL retornada.
- Função `clearLost(petID)` faz `DELETE /pets/{id}/lost`, fecha o modal, atualiza UI.
- Tratar `msg.type === "lost_status"` no handler SSE existente para sincronizar entre abas.

**Página pública (`lost.html`):**

- HTML novo na raiz, servido como qualquer arquivo estático. JS lê o token da URL via `window.location.pathname.split('/').pop()` ou parâmetro de query.
- Mapa Leaflet sem sidebar; chama `GET /lost/{token}/state` no boot e a cada 10s (polling — sem SSE para reduzir custo).
- Trail polyline + marker do pet + cabeçalho com nome/espécie/horário "perdido desde".

**Dependências:** apenas stdlib (`crypto/rand`, `encoding/hex`). Sem libs externas.

**Considerações:**

- **Concorrência:** mutações em `pet.LostToken` e `tokenIndex` precisam acontecer sob o mesmo `r.mu.Lock()`. Usar método dedicado no `Registry` em vez de modificar `*PetTracker` direto de fora.
- **Segurança:** 128 bits de entropia tornam impossível enumerar tokens; ao desmarcar, o token é descartado e qualquer requisição futura para `/lost/{token}` devolve 404.
- **Persistência:** sem SQLite (feature 03), o token vive em memória — restart perde o link. Documentar essa limitação no slide do modal.
- **Retrocompatibilidade:** os 3 campos novos no `Pet` são opcionais e omitempty no JSON; clientes antigos que ignoram esses campos continuam funcionando.

**Esforço estimado:** 1.5 sessões de aula (~2h15) — backend ~30min, frontend interno ~30min, página pública ~45min, polish e ajuda ~30min.

## Layout

**Sidebar (linha do pet):**

- Novo botão `<button data-act="lost" class="warn">🚨 Marcar como perdido</button>` ao lado de "Localizar" e "Definir zona".
- Cor: fundo `#fff`, borda `#e67e22`, texto `#cf6d10`. Hover: fundo `#fdebd0`. Fonte 11px (consistente com os botões existentes).
- Quando `pet.lost`, vira `<button data-act="found">✅ Marcar como encontrado</button>` com borda/texto verde `#2a7`.
- Linha do pet ganha classe `.lost` que aplica `border-left: 3px solid #e74c3c; padding-left: 6px;`.
- Badge novo: `.badge.lost { background: #fde0e0; color: #c0392b; }` mostrando "PERDIDO".

**Modal "Pet perdido"** (reaproveita estilos de `#help-overlay` e `#help-modal`):

```
┌──────────────────────────────────────┐
│ 🚨   Mia está marcada como perdida   │
│                                       │
│  Compartilhe esse link com vizinhos:  │
│  ┌─────────────────────────────────┐ │
│  │ http://localhost:8080/lost/8a3f… │ │
│  └─────────────────────────────────┘ │
│  [ 📋 Copiar link ]                   │
│                                       │
│  [ Compartilhar no WhatsApp ] (verde) │
│                                       │
│  [ Fechar ]                           │
└──────────────────────────────────────┘
```

- Largura 480px, raio 14px, sombra `0 18px 48px rgba(0,0,0,0.32)`.
- Estado vazio: enquanto a posição não chega, mostrar "Aguardando primeira atualização…" no link.
- Estado de erro: se `POST /pets/{id}/lost` falhar, mostrar caixa vermelha `#fde0e0`/`#c0392b` com a mensagem do servidor.

**Página pública `/lost/{token}` (`lost.html`):**

```
┌────────────────────────────────────────────┐
│ 🐱 Mia foi vista por último às 14:32       │  ← header verde #2a7, texto branco
│ Se você avistar, ligue para a dona          │
├────────────────────────────────────────────┤
│                                             │
│         [   Mapa Leaflet 100%   ]           │
│   • marker pulsante na última posição       │
│   • polyline com últimos 100 pontos         │
│                                             │
├────────────────────────────────────────────┤
│ Atualiza automaticamente a cada 10s    🔄   │  ← footer cinza #f6f7f9
└────────────────────────────────────────────┘
```

- Sem sidebar; mapa ocupa 100% da viewport menos header (60px) e footer (32px).
- Tipografia: `system-ui, sans-serif` (idem app principal).
- Estado vazio (sem histórico): texto centralizado "Sem dados de localização ainda" sobre o mapa cinza.
- Estado de erro 404: página estática "Esse link expirou ou não existe" + emoji 🚫.

**Slide novo no modal de ajuda** (após o slide 4 atual — vira slide 5):

- Emoji em círculo: 🚨
- Título (`<h2>`): "Pet perdido"
- Resumo (`<p class="summary">`): "Marque o pet como perdido e gere um link público para divulgar nas redes."
- Steps (`<ul class="steps">`):
  1. Na linha do pet, clique em **🚨 Marcar como perdido**.
  2. Copie o link gerado e cole em grupos do WhatsApp.
  3. Quando achar, clique em **✅ Marcar como encontrado** — o link expira.

## Critérios de pronto

- [ ] Struct `Pet` ganha campos `Lost`, `LostToken`, `LostSince` e o `Registry` ganha `tokenIndex` + métodos `MarkLost` / `ClearLost` / `LookupByToken`.
- [ ] Endpoints `POST /pets/{id}/lost`, `DELETE /pets/{id}/lost`, `GET /lost/{token}`, `GET /lost/{token}/state` testados via curl com sucesso e 404 nos casos de erro.
- [ ] Frontend: botão "Marcar como perdido" visível em cada pet, modal com link copiável e botão WhatsApp, badge "PERDIDO" e borda vermelha aplicada à linha.
- [ ] Página `lost.html` carrega o mapa, mostra o pet e atualiza via polling a cada 10s.
- [ ] Mensagem `type: "lost_status"` no SSE sincroniza estado entre abas.
- [ ] Slide "Pet perdido" adicionado ao container `.help-slides` do modal de ajuda.
- [ ] IDEIAS.md atualizado marcando "Modo perdido" como ✅.
- [ ] TASKS.md atualizado movendo a feature para "Concluídas" + entrada no histórico.
