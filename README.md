# Pet Tracker

Sistema de rastreamento de pets em tempo real com geofence, alertas, modo
perdido com link público, QR codes de avistamento, mural comunitário,
estatísticas de movimento e modo "celular como rastreador".

> Para a visão geral do projeto (motivação, features e decisões técnicas),
> veja **[SOBRE.md](./SOBRE.md)**. Para o caso de estudo completo da
> disciplina, veja **[docs/caso-estudo.pdf](./docs/caso-estudo.pdf)**.

---

## Requisitos

- **Go 1.18+** (testado em Go 1.26).
- Navegador moderno (Chrome, Firefox, Edge ou Safari recentes).
- Conexão com internet para carregar o mapa (OpenStreetMap + Leaflet via CDN).

Não há dependências externas no `go.mod` — o backend usa apenas a biblioteca
padrão.

## Como rodar

Clone (ou descompacte) o projeto, entre na pasta e execute:

```bash
go run .
```

Você verá:

```
Pet Tracker em http://localhost:8080
GET  /pets                  -> lista pets
POST /pets                  -> cria pet {name, species, color?}
...
```

Abra **http://localhost:8080** no navegador. Na primeira tela aparece o
tutorial em slides; siga os passos ou feche com `×` / `Esc`.

### Build de um binário

```bash
go build -o pet-tracker .
./pet-tracker
```

### Configuração por variáveis de ambiente

| Variável             | Padrão        | Descrição                                      |
| -------------------- | ------------- | ---------------------------------------------- |
| `PET_TRACKER_ADDR`   | `:8080`       | Endereço/porta de escuta                       |
| `PET_TRACKER_STORE`  | `store.json`  | Caminho do arquivo de persistência            |

Exemplo:

```bash
PET_TRACKER_ADDR=:9000 PET_TRACKER_STORE=/tmp/pets.json go run .
```

## Estrutura do projeto

```
pet-tracker/
├── main.go           # Backend HTTP em Go (1 arquivo, biblioteca padrão)
├── index.html        # Painel do dono (mapa, sidebar, ações, modais)
├── lost.html         # Página pública de pet perdido (/lost/{token})
├── sight.html        # Formulário público de avistamento (/sight/{token})
├── sender.html       # Celular como rastreador GPS (/sender)
├── mural.html        # Mural público de pets perdidos (/mural)
├── store.json        # Estado persistido em runtime (gerado)
├── go.mod
├── README.md         # Este arquivo
├── SOBRE.md          # Visão do projeto, features e arquitetura
├── IDEIAS.md         # Catálogo de ideias e roadmap
└── docs/
    ├── TASKS.md
    ├── caso-estudo.pdf
    └── 01..05-*.md   # Specs por feature
```

## Páginas

| Rota                  | Para quem                       | Função                                              |
| --------------------- | ------------------------------- | --------------------------------------------------- |
| `/`                   | Dono                            | Painel principal: mapa, lista de pets, ações       |
| `/sender`             | Dono (no celular)               | Envia GPS do navegador como tracker                |
| `/mural`              | Comunidade                      | Mural público de pets perdidos                     |
| `/lost/{token}`       | Quem recebeu o link             | Mapa público em tempo real do pet perdido          |
| `/sight/{token}`      | Quem escaneou o QR da coleira   | Formulário anônimo de reporte de avistamento       |

## API (endpoints HTTP)

```
GET    /pets                  Lista todos os pets
POST   /pets                  Cria pet { name, species, color? }
GET    /pets/{id}             Detalhes
PATCH  /pets/{id}             Edita nome/espécie/cor
DELETE /pets/{id}             Remove pet (incluindo histórico)

PUT    /pets/{id}/zone        Define zona segura { center_lat, center_lon, radius_meters }
DELETE /pets/{id}/zone        Remove zona

POST   /pets/{id}/lost        Marca como perdido, devolve { public_url, lost_token }
DELETE /pets/{id}/lost        Marca como encontrado

GET    /pets/{id}/stats       Estatísticas (distância, velocidade, tempo ativo)
GET    /pets/{id}/sightings   Avistamentos reportados via QR
GET    /pets/{id}/qr          Devolve { sight_token, sight_url } para gerar QR

GET    /position              Posição atual de todos os pets
POST   /position              Ingere posição { pet_id, latitude, longitude }
GET    /history?pet_id=1      Histórico de um pet (até 1000 pontos)

GET    /stream                Server-Sent Events em tempo real
GET    /api/mural             Lista pública de pets perdidos
GET    /healthz               Health check

GET    /lost/{token}/state    Estado JSON do pet perdido (consumido pelo mapa público)
POST   /sight/{token}         Reporta avistamento { latitude, longitude, note?, reporter? }
GET    /sight/{token}/state   Estado JSON visto pelo QR
```

## Atalhos de teclado (no painel)

| Tecla   | Ação                                |
| ------- | ----------------------------------- |
| `?`     | Abre o tutorial                     |
| `/`     | Foco na busca de pets               |
| `t`     | Alterna tema claro / escuro         |
| `Esc`   | Fecha modal aberta / cancela zona   |
| `←` `→` | Navega entre slides do tutorial     |

## Persistência

O estado é gravado em `store.json` (auto-save a cada 2 s sempre que algo muda).
No `start`, o servidor carrega esse arquivo de volta — pets, zonas, históricos,
tokens públicos e avistamentos sobrevivem a um restart. Para começar do zero,
basta deletar `store.json`.

## Limpando o estado

```bash
rm store.json
go run .
```

Na primeira execução sem `store.json`, o servidor cria dois pets de exemplo
(Rei Julian 🐶 e Mia 🐱) na Praça Coronel Pedro Osório (Pelotas / RS).

## Solução de problemas

- **"O mapa não aparece" / só carrega cinza:** verifique conexão com a
  internet (os tiles do mapa vêm do OpenStreetMap).
- **`/sender` não pega GPS:** Chrome exige HTTPS ou `localhost` para liberar
  geolocalização. Use `http://localhost:8080/sender` ou rode atrás de HTTPS.
- **Porta em uso:** rode com `PET_TRACKER_ADDR=:9000`.
