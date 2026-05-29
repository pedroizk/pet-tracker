# TASKS — Pet Tracker

Lista mestre de features do MVP. Cada item linka para o documento detalhado em `docs/`. Marque com `[x]` ao concluir cada feature.

**Convenção:** uma feature só pode ser marcada como concluída quando **todos** os critérios de pronto do seu `.md` correspondente estiverem verdadeiros.

## Concluídas

- [x] Multi-pet
- [x] Geofence + alertas no navegador
- [x] Modal de ajuda paginado + tutorial automático na 1ª visita
- [x] [Modo perdido + link público](./01-modo-perdido.md)
- [x] [Página celular sender](./02-celular-sender.md)
- [x] [Persistência (JSON em disco)](./03-persistencia-sqlite.md)
- [x] [Reportes via QR code (sighting)](./05-qr-sightings.md)
- [x] Dark mode + UI moderna com design tokens
- [x] Estatísticas por pet (distância, tempo ativo, velocidade média)
- [x] Edição e exclusão de pet
- [x] Mural público de pets perdidos (`/mural`)
- [x] Toasts globais e feedback visual no lugar de `alert()`
- [x] Sidebar responsivo (mobile drawer com hambúrguer)
- [x] Atalhos de teclado (`?`, `/`, `t`, `Esc`, ←/→ no tutorial)
- [x] Indicador de status SSE (conectando / ao vivo / reconectando)
- [x] Reconexão automática do SSE com heartbeat de 15s
- [x] Cores customizáveis por pet
- [x] Mais espécies (cão, gato, coelho, pássaro, outro)

## Pendentes (para evolução futura)

- [ ] [Contas de dono](./04-contas-de-dono.md) — autenticação por cookie/sessão; pré-requisito para variações multi-família e walker
- [ ] Detecção automática de passeio (movimento sustentado fora de casa = sessão de passeio)
- [ ] Heatmap dos lugares mais frequentados
- [ ] Lembretes de vacina / agenda veterinária
- [ ] Variante hardware: integração com coleira ESP32 + GPS (atualmente o app mocka via simulação ou sender)

## Histórico

Adicione uma linha aqui ao concluir cada item, no formato:

`- AAAA-MM-DD — feature X concluída — (resumo de 1 linha)`

- 2026-04-27 — Modo perdido + link público concluído — botão na sidebar marca pet como perdido, gera token público de 128 bits e abre `/lost/{token}` com mapa que atualiza por polling a cada 10s.
- 2026-05-25 — Página celular sender concluída — `/sender` usa `navigator.geolocation.watchPosition` e posta para `/position` num intervalo configurável (3–30s), com wake-lock e estatísticas em tempo real.
- 2026-05-25 — Persistência concluída — backend grava `store.json` (snapshot completo de pets/zonas/histórico/sightings/lost_state) com auto-save a cada 2s; carrega no startup; tokens públicos sobrevivem a restart.
- 2026-05-25 — QR sightings concluído — cada pet ganha `sight_token` único, modal de QR no painel imprime cartão para coleira, página `/sight/{token}` aceita reporte anônimo de localização + nota; sightings aparecem como pins 👁️ no `/lost/{token}` e no painel do dono.
- 2026-05-25 — UI v2 concluída — design tokens com dark mode, sidebar responsivo, toasts, modais de stats/QR/edit/confirm, header global com search e indicador SSE, atalhos de teclado, mais espécies, cores customizáveis, mural público em `/mural`.
