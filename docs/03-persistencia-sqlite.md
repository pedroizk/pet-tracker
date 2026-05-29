# Persistência SQLite

## Ideia

Hoje todo o estado do Pet Tracker vive em memória: pets, zonas seguras, histórico de posições e (em breve) tokens de modo perdido. Isso significa que **qualquer reinício do servidor zera tudo** — uma péssima cara para a apresentação ("aqui o app… ah, esqueci, perdi os pets de novo"). Persistir em SQLite resolve esse problema com baixíssimo overhead operacional: um único arquivo `.db` no disco, sem necessidade de servidor de banco.

O valor é triplo: (1) sobrevivência a restart, (2) aparência profissional ("dados salvos no servidor" vira slide do modal), (3) habilita as features 04 (contas de dono) e 05 (sightings) que precisam de tabelas de relacionamento e queries por dono.

**Cenário típico:** durante a apresentação, o professor pede para reiniciar o servidor para confirmar que não é só um demo bonito. Hoje, todos os pets desaparecem; é preciso recadastrar Rei Julian e Mia, redesenhar a zona, esperar o histórico crescer de novo. Com SQLite: `Ctrl+C` no terminal, `go run .`, abre o navegador — Rei Julian e Mia já estão lá, com a zona segura desenhada e os últimos 200 pontos do trail visíveis.

## Implementação

**Backend — novo arquivo `storage.go`:**

- `import "database/sql"` + `_ "modernc.org/sqlite"` (driver puro Go, sem CGO — compila no Windows sem dor).
- Struct `Store struct { db *sql.DB }`.
- Construtor `NewStore(path string) (*Store, error)`:
  - Abre conexão com `sql.Open("sqlite", path)`.
  - Configura `db.SetMaxOpenConns(1)` (SQLite serializa escrita) ou usa `WAL` para permitir leituras concorrentes: `db.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA foreign_keys=ON;")`.
  - Roda migrations inline:
    ```sql
    CREATE TABLE IF NOT EXISTS pets(
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      name TEXT NOT NULL,
      species TEXT NOT NULL,
      lost INTEGER NOT NULL DEFAULT 0,
      lost_token TEXT,
      lost_since DATETIME,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );
    CREATE INDEX IF NOT EXISTS pets_lost_token ON pets(lost_token);

    CREATE TABLE IF NOT EXISTS zones(
      pet_id INTEGER PRIMARY KEY REFERENCES pets(id) ON DELETE CASCADE,
      center_lat REAL NOT NULL,
      center_lon REAL NOT NULL,
      radius_meters REAL NOT NULL
    );

    CREATE TABLE IF NOT EXISTS positions(
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      pet_id INTEGER NOT NULL REFERENCES pets(id) ON DELETE CASCADE,
      latitude REAL NOT NULL,
      longitude REAL NOT NULL,
      timestamp DATETIME NOT NULL,
      step INTEGER NOT NULL,
      outside_zone INTEGER NOT NULL DEFAULT 0,
      source TEXT
    );
    CREATE INDEX IF NOT EXISTS positions_pet_ts ON positions(pet_id, timestamp DESC);
    ```
- Métodos: `InsertPet(name, species) (id int64, err)`, `DeletePet(id) error`, `LoadPets() ([]Pet, error)`, `UpsertZone(petID, z SafeZone) error`, `DeleteZone(petID) error`, `InsertPosition(p Position) error`, `LoadRecentPositions(petID int64, limit int) ([]Position, error)`, `UpdateLost(petID, lost, token, since) error`, `Prune(olderThan time.Time) (int64, error)`.

**Backend — mudanças em `main.go`:**

- `Registry` ganha campo `store *Store`. `NewRegistry(store *Store) *Registry`.
- `Registry.AddPet`: chama `store.InsertPet` antes de criar o tracker em memória; usa o `id` retornado pelo banco em vez do contador atômico.
- `Registry.RemovePet`: chama `store.DeletePet` antes de deletar do mapa.
- `Registry.SetZone`: chama `store.UpsertZone` ou `store.DeleteZone` conforme.
- `applyPosition` (chamado dentro de `UpdatePosition`): após `applyPosition` retornar, fazer `r.store.InsertPosition(stored)` em uma **goroutine separada** (não bloqueia o broadcast). Para evitar enchente, agrupar inserts em transação a cada 1s via `chan Position` e `time.Tick`.
- Novo `Registry.LoadFromStore()` chamado no boot: busca pets, zonas e últimos 200 pontos de cada um, popula a memória.
- `main()`:
  ```go
  store, err := NewStore("./pet-tracker.db")
  if err != nil { log.Fatal(err) }
  defer store.Close()
  reg := NewRegistry(store)
  if err := reg.LoadFromStore(); err != nil { log.Fatal(err) }
  if len(reg.ListPets()) == 0 {
      reg.AddPet("Rei Julian", SpeciesDog)
      reg.AddPet("Mia", SpeciesCat)
  }
  go startSimulation(reg)
  go pruneLoop(store)  // a cada 1h, store.Prune(time.Now().Add(-7*24*time.Hour))
  ```

**Frontend (`index.html`):** sem mudanças funcionais — `loadPets()` e `loadHistory()` já fazem a coisa certa. Pequeno indicador visual: rodapé da sidebar com `<small>💾 Dados salvos no servidor</small>` (cor `#888`, fonte 11px) para reforço.

**Dependências:**

- `modernc.org/sqlite` (puro Go, MIT, sem CGO).
- `database/sql` (stdlib).

**Considerações:**

- **Concorrência:** SQLite serializa escritas. Com WAL, leituras não bloqueiam. O `Registry.mu` já protege a memória; o `Store` gerencia o DB. Inserts de posição em batch (transação a cada 1s) evita gargalo se múltiplos pets se moverem juntos.
- **Persistência:** confirmar que `pet-tracker.db`, `pet-tracker.db-wal` e `pet-tracker.db-shm` estão no `.gitignore` (atualmente `.gitignore` tem 18 bytes — verificar conteúdo e adicionar se faltar).
- **Retrocompatibilidade:** o JSON de saída de todos os endpoints permanece idêntico; clientes não percebem a mudança.
- **Migração:** na primeira execução com banco vazio, manter os pets de seed (Rei Julian, Mia) para que `go run .` continue plug-and-play.
- **Pruning:** posições > 7 dias são deletadas a cada hora — mantém o `.db` enxuto sem perder o histórico recente.

**Esforço estimado:** 1.5 sessões (~2h15) — `storage.go` completo (~1h), integração no `Registry` (~45min), boot/seed/pruning (~20min), teste de restart (~10min).

## Layout

Esta feature é majoritariamente **invisível** ao usuário — o objetivo é justamente "tudo continua funcionando depois de fechar o navegador". Mudanças mínimas:

**Sidebar (rodapé):**

- Adicionar `<small id="storage-hint">💾 Dados salvos no servidor</small>` no fim do `<aside id="sidebar">`.
- Estilo: `font-size: 11px; color: #888; display: block; margin-top: 18px; text-align: center; opacity: 0.7;`.

**Pet row (efeito visual sutil de "carregado do disco"):**

- No primeiro paint após `loadHistory()`, aplicar `entry.trail` com `dashArray: '6 4'` por 5 segundos, depois remover. Sinaliza visualmente que o trail veio do banco, não do live. Opcional, mas dá feedback ao usuário de que o que está vendo é histórico real persistido.

**Slide novo no modal de ajuda** (após "Celular como rastreador" — vira slide 7):

- Emoji em círculo: 💾
- Título (`<h2>`): "Tudo salvo automaticamente"
- Resumo (`<p class="summary">`): "Pets, zonas e rastros recentes ficam guardados — basta reabrir o app."
- Steps (`<ul class="steps">`):
  1. Cadastre o pet **uma única vez**.
  2. Defina a zona segura **uma única vez**.
  3. Feche e abra o app — está **tudo lá**.

## Critérios de pronto

- [ ] Arquivo `storage.go` criado com `Store`, migrations, e métodos CRUD para pets, zonas, posições, lost-state.
- [ ] `Registry` recebe `*Store` no construtor e chama o storage em todas as mutações.
- [ ] `Registry.LoadFromStore()` rehidrata pets, zonas e últimos 200 pontos por pet no boot.
- [ ] Restart do servidor preserva pets, zonas, trail e estado de "perdido" — testado via `Ctrl+C` + `go run .` + abrir navegador.
- [ ] WAL mode habilitado (`PRAGMA journal_mode=WAL`); sem deadlocks após 5min com 5+ pets se movendo.
- [ ] Pruning de posições > 7 dias rodando em background a cada hora.
- [ ] `pet-tracker.db*` no `.gitignore`.
- [ ] Pets de seed só são criados quando o banco está vazio.
- [ ] Slide "Tudo salvo automaticamente" no modal de ajuda.
- [ ] IDEIAS.md atualizado marcando persistência como ✅.
- [ ] TASKS.md atualizado movendo a feature para "Concluídas" + histórico.
