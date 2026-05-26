# Sobre o Pet Tracker

> Um serviço que ajuda donos a **encontrar pets perdidos** por meio de
> rastreamento GPS, alertas de zona segura e uma **rede comunitária de
> avistamentos via QR code** — funciona com coleira GPS, com um celular
> antigo amarrado na coleira, ou apenas com um QR code impresso.

---

## Motivação

Pets se perdem todos os dias. As soluções existentes (chip de identificação,
cartazes no bairro, grupos de WhatsApp) são lentas e dependem de sorte. As
coleiras GPS comerciais são caras (R$ 200–500) e exigem assinatura mensal.

O **Pet Tracker** propõe três caminhos de adoção, do mais barato ao mais
elaborado, todos integrados ao mesmo backend e à mesma interface:

| Caminho                | Custo           | Como funciona                                      |
| ---------------------- | --------------- | -------------------------------------------------- |
| **QR na coleira**      | ~R$ 5           | Cartão impresso com QR único — quem acha, reporta |
| **Celular como tracker** | R$ 0 (reuso)  | Celular antigo amarrado na coleira via web        |
| **Coleira GPS dedicada** | ~R$ 80–200    | Hardware ESP32 + módulo GPS (não incluso)         |

O diferencial é a **rede comunitária**: mesmo um pet **sem GPS** pode ser
encontrado porque qualquer estranho pode escanear o QR e mandar a localização
anonimamente ao dono.

---

## Features

### Pra todo dono

- **Multi-pet** com 5 espécies (cão, gato, coelho, pássaro, outro) e cor
  customizável por pet.
- **Mapa em tempo real** (Leaflet + OpenStreetMap) com marcador, rastro
  histórico e zona segura visíveis.
- **Geofence**: dois cliques no mapa desenham uma zona; quando o pet sai,
  dispara banner, beep e notificação do navegador.
- **Estatísticas**: distância total, posições registradas, velocidade média,
  tempo de tracking e quantas vezes saiu da zona.
- **Histórico** de até 1000 posições por pet, persistido em disco.
- **Sidebar responsiva** com busca de pets em tempo real.
- **Dark mode** com persistência da preferência.

### Quando o pet se perde

- **Modo perdido**: um clique gera um link público (token de 128 bits) que
  mostra mapa ao vivo, rastro recente e pins de avistamentos. Pronto pra
  colar em grupos do WhatsApp.
- **Mural público** (`/mural`): página comunitária que agrega todos os pets
  em modo perdido na sua cidade, atualizada a cada 8 s.
- **Compartilhamento via WhatsApp** em um clique.

### Rede comunitária — QR de avistamento

- Cada pet tem um **`sight_token`** único atribuído na criação.
- O modal de QR no painel **imprime** um cartão com o QR e o nome do pet,
  pronto pra coleira.
- Quem escaneia abre `/sight/{token}`, vê o nome do pet e um botão
  **"Reportar avistamento"** que captura a localização atual (com permissão)
  e aceita uma nota opcional ("estava perto da praça às 14:00") e contato
  (opcional — pode ser anônimo).
- O avistamento aparece como pin 👁️ no mapa do dono e na página pública
  `/lost/{token}` em tempo real (via SSE para o dono, polling para os outros).

### Celular antigo como tracker

- Página `/sender` usa `navigator.geolocation.watchPosition` e posta para
  `/position` num intervalo configurável (3 s, 5 s, 10 s ou 30 s).
- Usa **Wake Lock API** quando disponível para a tela não dormir.
- Mostra estatísticas em tempo real: número de envios, precisão GPS atual,
  últimas coordenadas e hora.
- Custo zero — basta um celular Android antigo amarrado na coleira.

### Boa experiência

- **Tutorial em 9 slides** que abre automaticamente ao entrar e pode ser
  reaberto pelo botão flutuante `?`.
- **Toasts** globais substituem `alert()`.
- **Atalhos de teclado**: `?` (ajuda), `/` (busca), `t` (tema), `Esc`,
  ←/→ (navegação no tutorial).
- **Indicador de status SSE** (ao vivo / reconectando / erro) sempre visível
  no header.
- **Reconexão automática** do stream com heartbeat de 15 s.

---

## Arquitetura

```
┌─────────────────────────────────────────────────────────┐
│                  Browser (cliente)                       │
│  index.html · lost.html · sight.html · sender.html      │
│  mural.html — todos vanilla JS + Leaflet                │
└──────────────┬──────────────────────────┬───────────────┘
               │ HTTP/JSON                │ SSE (/stream)
               ▼                          ▼
┌─────────────────────────────────────────────────────────┐
│           Backend (main.go — Go stdlib)                  │
│                                                          │
│   Registry  ←→  PetTracker (1 por pet)                  │
│      │            ├─ Position, History                  │
│      │            ├─ SafeZone                           │
│      │            ├─ Sightings                          │
│      │            └─ Lost state + token público         │
│      │                                                   │
│      ├─ broadcast SSE para todos os clients conectados  │
│      ├─ índices token → pet (lostIndex, sightIndex)     │
│      └─ persistência atômica (store.json)               │
└─────────────────────────────────────────────────────────┘
```

### Decisões técnicas

- **Sem dependências externas no Go.** Tudo na biblioteca padrão. O `go.mod`
  só declara o módulo, não lista nenhum pacote. Vantagem: `go run .`
  funciona em qualquer máquina com Go, sem `go mod download`.
- **Persistência em JSON em vez de SQLite.** Foi feita a escolha consciente
  de usar `store.json` com escrita atômica (`tmp` + rename) e auto-save a
  cada 2 s, em vez de uma engine SQL com `cgo`. Os volumes envolvidos (≤
  alguns milhares de pontos por pet) não justificam SQL e a portabilidade
  é máxima. Para escalar, trocar a implementação do `Registry.Save/Load` é
  uma alteração local de ~40 linhas.
- **Server-Sent Events em vez de WebSocket.** SSE é unidirecional (servidor →
  cliente), o que casa exatamente com o caso de uso (broadcast de posição e
  alertas), e atravessa proxies/firewalls como HTTP comum.
- **Tokens públicos opacos de 128 bits.** `lost_token` e `sight_token` são
  16 bytes aleatórios em hex (`crypto/rand`). O espaço de busca por força
  bruta é ~2¹²⁸, e o servidor 404 em qualquer formato inválido antes de
  tocar no índice, evitando timing attacks triviais.
- **Frontend vanilla.** Sem framework, sem bundler. CSS com variáveis
  (design tokens) habilita o dark mode com um único atributo no
  `<html>`. QR é gerado via `qrcode-generator` (CDN, ~10 KB).
- **Snapshot inicial no SSE.** Cada cliente novo recebe a posição corrente
  de todos os pets imediatamente, garantindo render rápido sem chamada
  extra ao `/position`.

### Concorrência

Cada `PetTracker` tem seu próprio `sync.RWMutex`. O `Registry` tem outro
mutex para o mapa de pets, índices de tokens e lista de clientes SSE. O
broadcast SSE faz cópia da lista de canais sob `RLock` e envia fora do
lock, evitando segurar a trava enquanto escreve em sockets. Canais de
cliente são bufferizados (`128`); se um cliente lento atrasa, mensagens
para ele são dropadas em vez de bloquearem o sistema todo.

---

## Stack

| Camada     | Tecnologia                                          |
| ---------- | --------------------------------------------------- |
| Backend    | Go 1.18+ (stdlib pura: `net/http`, `crypto/rand`)   |
| Frontend   | HTML + CSS + JavaScript vanilla                     |
| Mapas      | Leaflet 1.9 + OpenStreetMap (CDN)                   |
| QR Code    | qrcode-generator 1.4 (CDN)                          |
| Streaming  | Server-Sent Events                                  |
| Persistência | JSON em disco com escrita atômica                 |

---

## Roadmap pendente

Documentado em `IDEIAS.md`. Em ordem de impacto:

1. **Contas de dono** — sessões/cookies, pré-requisito para multi-família.
2. **Heatmap de lugares favoritos** — agregação visual do histórico.
3. **Modo "passeador"** — link temporário (X horas) para um dog walker.
4. **Lembretes de vacina** — agenda integrada ao perfil do pet.
5. **Hardware dedicado** — coleira ESP32 + GPS que posta no `/position`.

---

## Créditos

Projeto desenvolvido como caso de estudo escolar.
Documento de apresentação completo em `docs/caso-estudo.pdf`.
