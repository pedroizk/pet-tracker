# Contas de dono

## Ideia

Hoje qualquer pessoa que abrir o endereço do servidor enxerga **todos** os pets cadastrados. Isso impede colocar o app no ar, é constrangedor para a apresentação ("e se outro grupo abrir o link e ver nossos pets?") e não permite o cenário de "compartilhamento familiar" descrito no IDEIAS.md. **Contas de dono** resolvem isso adicionando autenticação por email/senha com sessão em cookie, e fazendo cada pet pertencer a um `owner_id`.

O valor é duplo: (1) viabiliza expor o app publicamente sem vazar dados, (2) habilita um caso de uso real — vários celulares da mesma família vendo os mesmos pets. Ainda no escopo do MVP, sem complicar com convites, recuperação de senha ou OAuth: email + senha + cookie de sessão de 30 dias é suficiente para a apresentação.

**Cenário típico:** Maria abre `pettracker.com`, é redirecionada para `/login.html`. Clica em "Criar conta", informa `maria@email.com` + senha. Cadastra a Mia, define a zona segura. À noite, Pedro (marido) abre o app no celular dele, faz login com a mesma conta — vê a Mia exatamente como Maria deixou. O vizinho que abre `pettracker.com` cai na tela de login e não vê nada. O link público da feature 01 (`/lost/{token}`) **continua acessível sem login** porque o ponto inteiro é divulgar.

## Implementação

**Backend (`main.go` + novo `auth.go`):**

- Structs:
  ```go
  type Owner struct {
      ID           int64     `json:"id"`
      Email        string    `json:"email"`
      PasswordHash []byte    `json:"-"`
      CreatedAt    time.Time `json:"created_at"`
  }
  type Session struct {
      Token     string    `json:"-"`
      OwnerID   int64     `json:"owner_id"`
      ExpiresAt time.Time `json:"expires_at"`
  }
  ```
- Tabelas SQLite (depende da feature 03):
  ```sql
  CREATE TABLE IF NOT EXISTS owners(
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email TEXT UNIQUE NOT NULL,
    password_hash BLOB NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
  );
  CREATE TABLE IF NOT EXISTS sessions(
    token TEXT PRIMARY KEY,
    owner_id INTEGER NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
    expires_at DATETIME NOT NULL
  );
  -- Adicionar coluna owner_id na tabela pets:
  ALTER TABLE pets ADD COLUMN owner_id INTEGER REFERENCES owners(id) ON DELETE CASCADE;
  CREATE INDEX IF NOT EXISTS pets_owner_id ON pets(owner_id);
  ```
- Migração de dados existentes: criar owner default `demo@local` / senha `demo` no boot se não existir; pets com `owner_id IS NULL` ficam atribuídos a esse owner.
- Endpoints novos:
  - `POST /auth/signup` body `{email, password}` → valida (email format, senha ≥ 8), hasheia com `bcrypt.GenerateFromPassword(pw, 10)`, insere em `owners`, cria sessão, seta cookie `pet_session`. Retorna `{owner: {id, email}}`.
  - `POST /auth/login` body `{email, password}` → busca owner, `bcrypt.CompareHashAndPassword`, gera novo token, seta cookie. Rate limit 5 tentativas / 5min por email (mapa em memória com `sync.Mutex`).
  - `POST /auth/logout` → deleta sessão atual, expira cookie.
  - `GET /auth/me` → retorna `{owner}` ou 401.
- Cookie:
  ```go
  http.SetCookie(w, &http.Cookie{
    Name: "pet_session", Value: token, Path: "/",
    HttpOnly: true, SameSite: http.SameSiteLaxMode,
    Expires: time.Now().Add(30*24*time.Hour),
  })
  ```
- Middleware `requireAuth(next http.HandlerFunc) http.HandlerFunc`:
  - Lê cookie `pet_session`, busca em `sessions` table, verifica `expires_at > now`.
  - Se válida, injeta `ownerID` no contexto: `ctx := context.WithValue(r.Context(), ctxKeyOwnerID, ownerID); next(w, r.WithContext(ctx))`.
  - Se inválida, retorna 401 JSON `{"error": "unauthenticated"}`.
- Aplicar middleware em: `/pets`, `/pets/`, `/position`, `/history`, `/stream`. **NÃO** aplicar em `/lost/`, `/sight/` (públicos), `/auth/*`, `/login.html`, `/sender` (sender precisa de outra estratégia, ver "Considerações").
- Mudanças no `Registry`:
  - `AddPet(name, species, ownerID)` — nova assinatura.
  - `ListPets(ownerID)` — filtra.
  - `GetTrackerForOwner(petID, ownerID)` — substitui `GetTracker` nos handlers autenticados; retorna `nil, false` se o pet não pertence.
  - SSE: `Subscribe(ownerID)` retorna canal que filtra mensagens cujo `pet_id` não pertence ao owner.

**Frontend (`index.html` + novo `login.html`):**

- `login.html`: formulário centralizado com tabs "Entrar" / "Criar conta". Submit faz `fetch('/auth/login'|'/auth/signup', { method: POST, body: JSON, credentials: 'same-origin' })`. Em sucesso, `window.location = '/'`.
- `index.html`: no boot, faz `fetch('/auth/me')`. Se 401, `window.location = '/login.html'`. Se 200, segue normal e mostra header com email do owner.
- Header novo na sidebar (acima do `<h1>🐾 Pet Tracker</h1>`):
  ```html
  <div id="user-bar">
    <span id="user-email">maria@email.com</span>
    <button id="logout-btn">Sair</button>
  </div>
  ```
- Wrapper global de `fetch`: se a resposta é 401, redireciona para `/login.html` automaticamente (evita repetir lógica).
- Adicionar `credentials: 'same-origin'` em todas as chamadas existentes (`loadPets`, `loadHistory`, `saveZone`, `deleteZone`, `EventSource('/stream')`).
- `EventSource` envia cookies por padrão se `withCredentials: true` — usar `new EventSource('/stream', { withCredentials: true })`.

**Dependências:**

- `golang.org/x/crypto/bcrypt` — adicionar via `go get golang.org/x/crypto/bcrypt`.
- `crypto/rand` (stdlib) — geração de session token.
- `context`, `net/http` (stdlib).

**Considerações:**

- **Segurança:**
  - Senha mínima 8 caracteres validada nos dois lados.
  - Cookie `HttpOnly` + `SameSite=Lax`. Em produção (HTTPS), também `Secure`.
  - Bcrypt cost 10 — nunca SHA1/MD5/plaintext.
  - Rate limit no login: max 5 tentativas / 5min por email (mapa em memória com janela deslizante).
  - Token de sessão: 32 bytes hex (256 bits) gerado com `crypto/rand`.
- **Persistência:** depende da feature 03. Sem SQLite, manter sessões em mapa de memória — login se perde no restart (aceitável para demo).
- **Concorrência:** tabela `sessions` é write-light (login/logout esporádicos). Sem otimização.
- **Página `/sender`** (feature 02): celular antigo amarrado na coleira não tem como fazer login. Solução: `sender.html` aceita um **token de envio** específico do pet (`POST /pets/{id}/sender-token` autenticado retorna token; URL fica `/sender?token=xyz` em vez de `?pet_id=1`). Backend valida o token sem cookie. Documentar essa mudança quando implementar a 04 — atualiza retroativamente a feature 02.
- **Retrocompatibilidade:**
  - Pets pré-existentes recebem `owner_id` do owner default `demo@local` / `demo`.
  - Endpoints continuam com mesmo formato JSON; só fica fechado atrás de auth.

**Esforço estimado:** 2 sessões (~3h) — auth handlers + middleware (~1h), ownership filtering em endpoints e Registry (~45min), `login.html` + redirects + user bar (~1h), retrofit do sender com token (~15min).

## Layout

**Página `/login.html`** (centralizada, fundo `#f6f7f9`, card branco 360px):

```
┌──────────────────────┐
│        🐾             │  ← logo verde grande
│   Pet Tracker         │
│                       │
│  ┌─────────────────┐  │
│  │ Entrar | Criar  │  │  ← tabs (active sublinhado verde)
│  └─────────────────┘  │
│                       │
│  Email                │
│  ┌─────────────────┐  │
│  │                 │  │
│  └─────────────────┘  │
│  Senha                │
│  ┌─────────────────┐  │
│  │                 │  │
│  └─────────────────┘  │
│                       │
│  [    Entrar    ]     │  ← botão verde 100% width
│                       │
│  ⚠ Email ou senha…    │  ← área de erro (vermelha)
└──────────────────────┘
```

- Tipografia: `system-ui, sans-serif` (idem app).
- Cores: borda `#ddd`, focus `#2a7`, botão `#2a7`/hover `#239065`.
- Erro: fundo `#fde0e0`, texto `#a83232`, fonte 12px, padding 8px, raio 4px.
- Estado disabled (durante POST): botão fica `opacity: 0.6; cursor: wait;`.
- Validação inline: email inválido → borda `#e74c3c` + mensagem abaixo do input.

**Sidebar do app — header novo:**

```
👤 maria@email.com    [ Sair ]
─────────────────────────────
🐾 Pet Tracker
```

- Email truncado com ellipsis se > 22 chars.
- Botão "Sair": fonte 11px, cor `#888`, hover sublinhado em `#a83232`.
- Estilo:
  ```css
  #user-bar {
    display: flex; justify-content: space-between; align-items: center;
    padding-bottom: 8px; border-bottom: 1px solid #e8e8e8;
    margin-bottom: 10px; font-size: 12px;
  }
  #user-email { color: #555; font-weight: 600; max-width: 180px;
                white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  #logout-btn { background: transparent; border: 0; color: #888;
                cursor: pointer; font-size: 11px; }
  #logout-btn:hover { color: #a83232; text-decoration: underline; }
  ```

**Slide novo no modal de ajuda** (após "Tudo salvo automaticamente" — vira slide 8):

- Emoji em círculo: 👤
- Título (`<h2>`): "Sua conta"
- Resumo (`<p class="summary">`): "Cada conta vê apenas os seus pets — compartilhe a senha com a família para ver juntos."
- Steps (`<ul class="steps">`):
  1. Crie sua conta com **email e senha**.
  2. Cadastre seus pets — só você vê.
  3. Compartilhe o login com quem mora junto para ver os mesmos pets.

## Critérios de pronto

- [ ] Tabelas `owners` e `sessions` criadas; coluna `owner_id` adicionada a `pets`.
- [ ] Endpoints `POST /auth/signup`, `POST /auth/login`, `POST /auth/logout`, `GET /auth/me` testados via curl com sucesso e erros (senha curta, email duplicado, credenciais inválidas).
- [ ] Middleware `requireAuth` aplicado em `/pets`, `/pets/`, `/position`, `/history`, `/stream`.
- [ ] Listagens filtram por `owner_id`: testado com 2 cookies diferentes — owner A não enxerga pets do owner B.
- [ ] `login.html` com tabs Entrar / Criar funcionando; submit redireciona para `/`.
- [ ] Wrapper global de `fetch` redireciona para `/login.html` em 401.
- [ ] Header `#user-bar` mostra email + botão "Sair" funcional.
- [ ] Senhas armazenadas com bcrypt (verificável diretamente no DB).
- [ ] Página pública `/lost/{token}` continua acessível sem login.
- [ ] Sender retrofit: `sender.html` aceita `?token=` específico do pet (sem cookie).
- [ ] Slide "Sua conta" no modal de ajuda.
- [ ] IDEIAS.md atualizado marcando "Contas de dono" como ✅.
- [ ] TASKS.md atualizado movendo a feature para "Concluídas" + histórico.
