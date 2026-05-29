# Página celular sender

## Ideia

O MVP atual simula a movimentação dos pets com um random walk no servidor — bom para demonstrar o conceito, mas ninguém compra um app que move bonecos no mapa. A **página celular sender** transforma o app em um rastreamento **real** sem custo de hardware: o dono pega um celular Android antigo da gaveta, abre uma URL no navegador, autoriza o GPS e prende o aparelho na coleira do cão. Cada movimento real do pet vira uma posição enviada para o backend via `navigator.geolocation.watchPosition` + `POST /position`.

O valor é triplo: (1) substitui a simulação por dados reais durante a apresentação, (2) é a forma mais barata de fazer rastreamento GPS de pets (custo zero se o celular já existe), (3) abre o caminho narrativo para a "coleira ESP32 + GPS" no futuro — o protocolo é o mesmo, muda só o cliente.

**Cenário típico:** João tem um Moto G4 velho na gaveta. No app desktop, na linha do "Rei Julian", clica em **📱 Modo celular**. Aparece um modal com QR code apontando para `http://192.168.1.10:8080/sender?pet_id=1`. João escaneia com a câmera do celular, autoriza o GPS quando o navegador pergunta, prende o celular na coleira com fita e solta o cachorro no quintal. No desktop, o marker do Rei Julian começa a se mover de verdade conforme o cachorro corre — sem nenhum atraso perceptível.

## Implementação

**Backend (`main.go`):**

- Adicionar campo opcional ao struct `Position`: `Source string \`json:"source,omitempty"\`` com valores `"simulator"` ou `"phone"`. Default `"simulator"`.
- Modificar `Registry.UpdatePosition` para aceitar a `Source` (parâmetro extra ou novo método `UpdatePositionFromPhone`).
- Modificar `positionHandler` no ramo `POST` para aceitar o campo opcional `source` no body. Se ausente, assume `"phone"` quando vier de cliente externo (heurística simples: header `User-Agent` contém "Mobile" — opcional, melhor deixar o cliente explícito).
- Não há novos endpoints; reusa `POST /position` e o file server estático para entregar `sender.html`.
- Considerar adicionar campos opcionais `accuracy_meters float64` e `battery_pct int` em `Position` para mostrar qualidade do sinal/bateria no painel — útil mas não bloqueia a feature.

**Frontend principal (`index.html`):**

- Pet row: nova ação `<button data-act="phone">📱 Usar celular</button>`.
- Função `openSenderModal(petID)`: abre modal mostrando QR code + URL textual + botão "Abrir em nova aba".
- Geração do QR code client-side: incluir `<script src="https://unpkg.com/qrcode-generator@1.4.4/qrcode.js">` e gerar SVG inline (12KB, sem dependência server-side).
- Indicador de "ao vivo" na pet row: quando a última posição tem `source: "phone"` recebida há menos de 30s, mostrar badge `📱 ao vivo` em verde claro.

**Página celular (`sender.html`):**

- Arquivo HTML novo na raiz, servido pelo `http.FileServer` existente.
- Layout mobile-first (viewport 100vh, fonte 16px+).
- JS:
  - Lê `pet_id` da query string (`new URLSearchParams(location.search).get('pet_id')`).
  - Se ausente, faz `fetch('/pets')` e mostra lista de cards com botão "Selecionar" que adiciona `?pet_id=X` na URL.
  - Se presente, busca o pet via `/pets` e mostra header com nome/emoji.
  - Botão grande "Iniciar envio" → chama `navigator.geolocation.watchPosition(onPosition, onError, {enableHighAccuracy: true, maximumAge: 0, timeout: 10000})`.
  - `onPosition(p)`: envia `POST /position` com `{pet_id, latitude: p.coords.latitude, longitude: p.coords.longitude, source: "phone"}`. Throttling: max 1 envio a cada 2s (descarta posições intermediárias).
  - Atualiza UI: contador de envios, accuracy, último timestamp.
  - Wakelock: `await navigator.wakeLock.request('screen')` para evitar a tela apagar (fallback silencioso se não suportado).
  - Botão "Pausar envio" cancela o watch e libera o wakelock.

**Dependências:**

- Stdlib Go (sem libs novas).
- Frontend: lib `qrcode-generator` (12KB, MIT, via unpkg).
- Browser APIs: `navigator.geolocation`, `navigator.wakeLock` (opcional).

**Considerações:**

- **HTTPS:** `navigator.geolocation` exige contexto seguro. `http://localhost` é exceção e funciona, mas IPs de rede local exigem HTTPS. Para a demo escolar, basta acessar pelo localhost; para uso em campo, túnel via `cloudflared tunnel --url http://localhost:8080` ou `ngrok http 8080`.
- **Bateria:** `enableHighAccuracy: true` consome muito — alertar no UI ("o celular vai descarregar mais rápido").
- **Concorrência:** `Registry.UpdatePosition` já é seguro com `r.mu`. Vários celulares enviando em paralelo já é suportado.
- **Throttling:** limitar a 1 envio / 2s no cliente evita inundação se o GPS oscilar.
- **Retrocompatibilidade:** o campo `source` é opcional — clientes antigos (incluindo o simulador atual) não precisam mudar.

**Esforço estimado:** 1 sessão de aula (~1h30) — backend trivial (~15min), `sender.html` com geolocation e wakelock (~45min), modal QR no app principal (~20min), polish (~10min).

## Layout

**Pet row (sidebar) — novo botão:**

- `<button data-act="phone">📱 Usar celular</button>` — mesmo padrão visual dos outros (fonte 11px, padding 3px 6px, border 1px `#bbb`, bg branco, hover `#f0f0f0`).
- Quando há sender ativo, badge inline `<span class="badge live">📱 ao vivo</span>` com fundo `#d6f5e3` e texto `#207244`, ao lado do badge de zona.

**Modal "Modo celular"** (overlay e modal idênticos ao `#help-modal`):

```
┌──────────────────────────────────────┐
│        📱 Usar celular antigo         │
│                                       │
│  Escaneie do celular antigo:          │
│                                       │
│        ███████  ███  ████             │
│        █ ███ █ █████ █ █              │
│        █ ███ █ █ █ █ █████            │  ← QR ~220x220 px
│        ███████ ██  █ ██████           │
│        █ █ █████ █ █ █ █ █            │
│                                       │
│  http://192.168.1.10:8080/sender?...  │
│  [ 📋 Copiar URL ] [ 🔗 Abrir aqui ]  │
│                                       │
│  Prenda o celular na coleira após     │
│  autorizar o GPS.                     │
└──────────────────────────────────────┘
```

- Largura 480px. URL truncada com ellipsis se passar de 60 chars.
- Botão "Abrir aqui" abre `/sender?pet_id={id}` em nova aba (útil pra testar no próprio computador).

**Página `/sender` (mobile-first):**

```
┌────────────────────────┐
│       🐶 Rei Julian    │  ← header verde #2a7, h=80px
│        ao vivo          │
├────────────────────────┤
│                        │
│         📡             │
│    Enviando…            │  ← estado dinâmico (Pausado / Erro)
│                        │
│   Última: agora        │
│   Precisão: 8 m         │
│   Enviados: 142         │
│   Bateria: 78 %         │
│                        │
│                        │
├────────────────────────┤
│  [   Pausar envio   ]  │  ← botão fixo no rodapé, h=60px
└────────────────────────┘
```

- Cor de fundo: `#f1faf5` quando ativo, `#f6f7f9` quando pausado, `#fde0e0` em erro.
- Botão fixo no rodapé: verde `#2a7` ("Iniciar") ou vermelho `#c0392b` ("Pausar"); fonte 18px, full width, raio 8px.
- Ícone 📡 grande (60px) animado com pulse leve quando ativo.
- **Estado erro de permissão:** ícone ⚠️ + texto "Autorize a localização nas configurações do navegador" + link "Como autorizar?".
- **Estado sem `pet_id`:** lista vertical de cards (`.pet-row` adaptado para mobile, full width) com botão "Selecionar" que injeta `?pet_id=X`.

**Slide novo no modal de ajuda** (após "Pet perdido" — vira slide 6):

- Emoji em círculo: 📱
- Título (`<h2>`): "Celular como rastreador"
- Resumo (`<p class="summary">`): "Use um celular antigo preso na coleira para rastrear de verdade — sem hardware extra."
- Steps (`<ul class="steps">`):
  1. Na linha do pet, clique em **📱 Usar celular**.
  2. Escaneie o **QR code** com o celular antigo.
  3. Autorize a localização e prenda o celular na coleira.

## Critérios de pronto

- [ ] Campo `Source` adicionado ao `Position` (com `omitempty`); `Registry.UpdatePosition` propaga a origem.
- [ ] Endpoint `POST /position` aceita `source` no body; testado via curl com `{pet_id, latitude, longitude, source: "phone"}`.
- [ ] Arquivo `sender.html` criado, servido em `/sender`, abre corretamente em http://localhost.
- [ ] Geolocation funciona com `pet_id` na query string e envia posições reais.
- [ ] Modal "Modo celular" no app principal mostra QR code apontando para a URL correta.
- [ ] Tratamento de erro de permissão exibe mensagem clara ao usuário.
- [ ] Throttling de 2s entre envios implementado no cliente.
- [ ] Badge "📱 ao vivo" aparece na pet row quando há sender ativo.
- [ ] Slide "Celular como rastreador" no modal de ajuda.
- [ ] IDEIAS.md atualizado marcando "Modo celular como tracker" como ✅.
- [ ] TASKS.md atualizado movendo a feature para "Concluídas" + histórico.
