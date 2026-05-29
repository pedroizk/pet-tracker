# Reportes via QR code (sighting reports)

## Ideia

Esta é a feature **diferencial** do produto: mesmo sem GPS na coleira, qualquer estranho que encontre o pet pode escanear um QR code físico amarrado na coleira → abre uma página simples no navegador → toca em um botão → envia a posição GPS dele anonimamente para o dono. **O QR code é o dispositivo de rastreamento**. Custo R$5 (um chaveirinho de QR impresso) versus R$200 de uma coleira GPS comercial.

O valor é enorme em três frentes: (1) viabiliza o produto para donos que não querem investir em hardware caro; (2) cria efeito de rede — quanto mais gente conhece o app, mais útil cada sighting fica; (3) é a feature "uau" da apresentação — demonstra raciocínio de produto, não só técnica.

**Cenário típico:** Mia foge no domingo à tarde e some por 6 horas. O Sr. Carlos da rua de cima encontra a gata no jardim dele, vê uma plaquinha de QR pendurada na coleira. Aponta a câmera do celular, abre a página `pettracker.com/sight/8a3f...`. A página mostra "Você encontrou a Mia! Toque para avisar a dona". Ele toca, autoriza GPS uma única vez, opcionalmente tira uma foto e digita "ela está bem, no meu jardim". Maria recebe **instantaneamente** um alerta no app desktop com a posição exata, foto e mensagem — corre até o endereço e busca a Mia em 5 minutos.

## Implementação

**Backend (`main.go` + tabela nova):**

- Adicionar campo permanente ao struct `Pet`: `SightingToken string \`json:"sighting_token,omitempty"\``. Gerado no `AddPet` via `crypto/rand` (16 bytes hex), nunca expira (diferente do `LostToken` que é efêmero).
- Adicionar índice secundário no `Registry`: `sightingIndex map[string]int64`.
- Migração: na primeira execução pós-deploy, gerar `sighting_token` para pets que ainda não têm.
- Struct novo:
  ```go
  type Sighting struct {
      ID              int64     `json:"id"`
      PetID           int64     `json:"pet_id"`
      Latitude        float64   `json:"latitude"`
      Longitude       float64   `json:"longitude"`
      Note            string    `json:"note,omitempty"`
      PhotoPath       string    `json:"photo_path,omitempty"`
      ReporterContact string    `json:"reporter_contact,omitempty"`
      Timestamp       time.Time `json:"timestamp"`
      Resolved        bool      `json:"resolved"`
  }
  ```
- Tabela SQLite (depende da feature 03):
  ```sql
  ALTER TABLE pets ADD COLUMN sighting_token TEXT UNIQUE;
  CREATE INDEX IF NOT EXISTS pets_sighting_token ON pets(sighting_token);

  CREATE TABLE IF NOT EXISTS sightings(
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    pet_id INTEGER NOT NULL REFERENCES pets(id) ON DELETE CASCADE,
    latitude REAL NOT NULL,
    longitude REAL NOT NULL,
    note TEXT,
    photo_path TEXT,
    reporter_contact TEXT,
    timestamp DATETIME NOT NULL,
    resolved INTEGER NOT NULL DEFAULT 0
  );
  CREATE INDEX IF NOT EXISTS sightings_pet_id_ts ON sightings(pet_id, timestamp DESC);
  ```
- Endpoints novos:
  - `GET /sight/{token}` → handler `sightPagePublicHandler` que serve `sight.html` (público, sem auth).
  - `GET /sight/{token}/pet` → JSON read-only com `{id, name, species, lost: bool}` para a página pública mostrar nome.
  - `POST /sight/{token}` body multipart com campos: `latitude`, `longitude`, `note?`, `contact?`, `photo?` (file). Sem auth. Valida tamanho da foto (max 5MB), magic bytes JPEG/PNG, salva em `./uploads/sightings/{id}.jpg`. Rate limit por IP: 3 reports / hora (mapa em memória).
  - `GET /pets/{id}/sightings` (autenticado) → lista sightings ordenadas DESC.
  - `POST /pets/{id}/sightings/{sid}/resolve` (autenticado) → marca como resolvida.
  - `GET /uploads/sightings/{id}.jpg` (autenticado, owner do pet) → serve foto.
- StreamMessage ganha `Type: "sighting"` com campo `Sighting *Sighting`.
- Adicionar lógica em `Registry`: novo método `RecordSighting(petID, sighting Sighting)` que persiste, faz broadcast SSE filtrado pelo dono e devolve a sighting com ID atribuído.

**Frontend principal (`index.html`):**

- Pet row: novo botão `<button data-act="qr">🔗 Imprimir QR</button>` ao lado dos demais.
- Modal "Imprimir QR": mostra QR code grande (240x240px) apontando para `/sight/{sighting_token}` + URL textual + botão "🖨 Imprimir" que dispara `window.print()` com CSS `@media print` específico (apenas o QR + nome do pet visíveis).
- Painel novo na sidebar (entre "Pets" e o final):
  ```html
  <h2>📍 Avistamentos <span id="sightings-badge">3</span></h2>
  <div id="sightings-list"></div>
  ```
- Função `loadSightings()`: agrega `GET /pets/{id}/sightings` para todos os pets ou cria endpoint agregador `GET /sightings` autenticado.
- Cards de sighting: timestamp, foto thumbnail (se houver), nota, botão "Ver no mapa" (centraliza marker), botão "Resolver".
- Marker no mapa por sighting: `L.divIcon` com 📍 amarelo grande; popup com detalhes.
- Notificação browser quando chega `msg.type === "sighting"` via SSE: `new Notification('Avistamento', { body: ${pet.name} foi visto, tag: sighting-${sid} })`.
- Beep diferente do alerta de zona (mais agudo) para distinguir.

**Página pública (`sight.html`):**

- Arquivo novo na raiz, servido por path filtrado (`/sight/{token}` mapeia para `sight.html`).
- JS lê token de `window.location.pathname.split('/').pop()`.
- Boot: `fetch('/sight/{token}/pet')` para buscar nome/espécie. Se 404, mostra "Esse link não existe".
- Botão grande "📍 Avisar o dono":
  - `navigator.geolocation.getCurrentPosition(success, error, { enableHighAccuracy: true, timeout: 10000 })`.
  - `success(p)`: monta `FormData` com `latitude`, `longitude`, `note`, `contact`, `photo`. `fetch('/sight/{token}', { method: POST, body: form })`. Mostra tela de sucesso.
  - `error`: mostra fallback com input `<textarea>` "Onde você está?" que é enviado como `note` (sem coordenadas).
- Campos opcionais collapsable (acordeão simples): "Adicionar foto", "Adicionar nota", "Meu telefone".
- Tela de sucesso: emoji 💚 grande + "Obrigado! O dono foi avisado".

**Geração de QR code:**

- Cliente: `<script src="https://unpkg.com/qrcode-generator@1.4.4/qrcode.js">` (já incluso pela feature 02). Gera SVG inline.

**Dependências:**

- Backend: `mime/multipart` (stdlib), `os` (stdlib).
- Frontend: `qrcode-generator` (12KB, já adicionado na feature 02).
- Browser APIs: `navigator.geolocation` (já usado), `Notification`.

**Considerações:**

- **Segurança:**
  - Endpoint `POST /sight/{token}` é público — atacar é trivial. Mitigações: rate limit 3/hora/IP (mapa em memória); limite 5MB no upload; validação de magic bytes (JPEG `FF D8 FF`, PNG `89 50 4E 47`); arquivos salvos em `./uploads/sightings/` com nome `{id}.jpg` (nunca o nome enviado pelo usuário).
  - Token de sighting permanente: 128 bits de entropia. Aceitável para ataque de enumeração.
- **Privacidade:** o reporter envia GPS dele — explicitar no UI ("sua localização será compartilhada"); **não** armazenar IP do reporter no DB; `reporter_contact` é opcional.
- **Concorrência:** inserts esporádicos, sem hotspot.
- **Persistência:** depende da feature 03; sem SQLite a feature funciona mas perde dados no restart.
- **Retrocompatibilidade:** pets pré-existentes recebem `sighting_token` no boot via migration.
- **Resolução:** `resolved=true` esconde o card da lista mas mantém histórico no banco — o dono ainda pode auditar avistamentos antigos.

**Esforço estimado:** 2 sessões (~3h) — backend + storage + upload (~1h), `sight.html` com geolocation/upload (~45min), painel + QR no app (~1h), notificação SSE + polish (~15min).

## Layout

**Pet row (sidebar) — novo botão:**

- `<button data-act="qr">🔗 Imprimir QR</button>` (mesmo padrão dos demais).

**Modal "Imprimir QR":**

```
┌──────────────────────┐
│   🐶 Rei Julian      │
│                      │
│   ███▓▓█▓██████      │
│   ███▓ ██▓██▓██      │
│   ███▓▓█▓██████      │  ← QR 240x240 px
│   ▓██▓██▓▓████▓      │
│   ███▓▓█▓██████      │
│                      │
│   pettracker.com/    │
│   sight/8a3f…        │
│                      │
│  [ 🖨 Imprimir ]     │
│  [ Fechar ]          │
└──────────────────────┘
```

- CSS `@media print`: esconde tudo exceto `.qr-print-area` (QR + nome do pet em fonte grande). Página A6 ou cartão de visita.

**Sidebar — novo painel "Avistamentos":**

```
📍 AVISTAMENTOS  [3]
─────────────────────
┌───────────────────┐
│ 🕒 14:32           │
│ "Vi a Mia no      │
│  parque, está     │
│  bem"             │
│ [📷 thumbnail 60] │
│ [Ver] [Resolver]  │
└───────────────────┘
┌───────────────────┐
│ 🕒 09:18           │
│ "Estava na rua…"  │
│ [Ver] [Resolver]  │
└───────────────────┘
```

- Badge `[3]` em vermelho `#c0392b` / branco quando há não resolvidos; fonte 11px; raio 10px.
- Cards: fundo `#fffbe6`, borda esquerda 3px `#f1c40f` (amarelo "atenção"); padding 8px; raio 6px; gap 8px.
- Foto thumbnail 60x60px (object-fit cover, raio 4px).
- Botões "Ver" / "Resolver": fonte 11px, padrão dos outros botões da pet row.
- Estado vazio: texto cinza "Nenhum avistamento ainda" centralizado, fonte 12px.

**Mapa — sighting markers:**

- `L.divIcon` com 📍 amarelo (`#f1c40f`) tamanho 32px, tooltip com horário.
- Popup ao clicar: nome do pet + timestamp + nota + foto (se houver) + botão "Marcar resolvido".
- Markers de sighting resolvido: opacidade 0.4.

**Página `/sight/{token}` (`sight.html`, mobile-first):**

```
┌────────────────────┐
│  Você encontrou:   │  ← header verde #2a7
│       🐱 Mia       │
│   Gato perdido     │
│  desde 14:32       │
├────────────────────┤
│                    │
│   ┌─────────────┐  │
│   │     📍      │  │  ← botão grande verde
│   │  Avisar o   │  │     full width
│   │    dono     │  │     h=80px
│   └─────────────┘  │
│                    │
│ ▾ Adicionar foto   │  ← acordeão
│ ▾ Adicionar nota   │
│ ▾ Meu telefone     │
│                    │
│  Sua localização   │
│  será partilhada   │  ← disclaimer cinza, fonte 11px
│  com a dona.       │
│                    │
└────────────────────┘
```

- Tela de sucesso: emoji 💚 grande (80px), texto "Obrigado! A dona foi avisada onde você viu a Mia.", fundo verde claro `#e8f8ef`.
- Tela de erro de GPS: ícone ⚠️ + "Não conseguimos pegar sua localização" + textarea "Onde você está?" + botão "Enviar mesmo assim".
- Tela 404: emoji 🚫 + "Esse link não existe" + sem ações.

**Slide novo no modal de ajuda** (após "Sua conta" — vira slide 9, último):

- Emoji em círculo: 📍
- Título (`<h2>`): "Reportes por QR code"
- Resumo (`<p class="summary">`): "Quem encontrar seu pet escaneia o QR da coleira e te avisa onde — sem precisar GPS no pet."
- Steps (`<ul class="steps">`):
  1. Imprima o QR clicando em **🔗 Imprimir QR** na linha do pet.
  2. Pendure na coleira.
  3. Você recebe alerta com a localização quando alguém escaneia.

## Critérios de pronto

- [ ] Tabela `sightings` + coluna `sighting_token` em `pets`; tokens gerados para pets pré-existentes.
- [ ] Endpoints `GET /sight/{token}`, `GET /sight/{token}/pet`, `POST /sight/{token}` (multipart), `GET /pets/{id}/sightings`, `POST /pets/{id}/sightings/{sid}/resolve` testados via curl.
- [ ] Página `sight.html` funcional com geolocation + upload de foto + nota + contato.
- [ ] Rate limit por IP (3 reports/hora) implementado.
- [ ] Validação de foto (5MB max + magic bytes JPEG/PNG) impede upload malicioso.
- [ ] Painel "Avistamentos" na sidebar com cards + badge contador de não resolvidos.
- [ ] Marker no mapa por sighting com popup detalhado.
- [ ] Modal "Imprimir QR" com QR code 240x240 + CSS `@media print` funcional.
- [ ] Mensagem `type: "sighting"` no SSE dispara notificação browser + beep para o dono.
- [ ] Slide "Reportes por QR code" no modal de ajuda.
- [ ] IDEIAS.md atualizado marcando "Reportes via QR code" como ✅.
- [ ] TASKS.md atualizado movendo a feature para "Concluídas" + histórico.
