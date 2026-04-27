# Pet Tracker — Catálogo de Ideias

Lista de funcionalidades possíveis para evoluir o projeto a partir do MVP atual
(múltiplos pets + geofence + alertas). Cada item indica o **valor para o usuário**,
não apenas a feature técnica.

---

## 1. Segurança — o "achar meu pet"

- **Geofence + alertas.** ✅ Implementado. Dono define uma zona segura; quando o
  pet sai, dispara banner + beep + notificação do navegador.
- **Modo "perdido".** ✅ Implementado. Um botão coloca o pet em estado "perdido"
  e gera um link público (somente leitura) com a última localização e o rastro
  recente. Dono cola o link em grupos do bairro no WhatsApp.
- **Última atualização visível.** "Offline desde 14:32" — o dono distingue
  problema do dispositivo de problema do pet.
- **Aviso de bateria fraca.** E-mail / push quando o coleira cai abaixo de 20%.

## 2. Atividade e saúde — "Fitbit para pets"

- **Distância diária / passos / minutos ativos**, com gráfico semanal.
  Veterinários perguntam isso — agora o app responde.
- **Heatmap dos lugares favoritos.** Onde o gato dorme? Onde o cachorro
  costuma fazer xixi nos passeios? (Divertido + insight.)
- **Detecção automática de passeio.** Movimento sustentado longe de casa vira
  uma sessão registrada com rota, distância e duração.

## 3. Multi-usuário / compartilhamento familiar

- **Contas de dono + convites.** Pet pertence a um lar; vários celulares
  visualizam/editam. Mãe, pai e filhos veem o Rex.
- **Modo passeador / pet sitter.** Link de acesso temporário (X horas) para
  entregar ao dog walker.

## 4. Comunidade / efeito de rede

- **Mural de pets perdidos.** Feed público de pets em modo "perdido" na sua
  cidade, com pin da última localização. Quem avistar pode reportar.
- **Reportes via QR code (sighting reports).** Estranho que encontra o pet
  escaneia o QR da coleira → abre página que envia anonimamente a
  localização atual ao dono. **Diferencial enorme:** dispensa GPS no
  dispositivo. O QR é o dispositivo.

## 5. Integrações com cuidado veterinário

- **Lembretes de vacinas e consultas** por pet.
- **Histórico de peso** com gráfico.
- **Compartilhar com vet.** PDF dos últimos 30 dias de atividade para enviar
  ao veterinário.

## 6. Hardware — sustenta o discurso de "produto real"

- **Coleira com ESP32 + módulo GPS.** Bateria, posta no `/position` via WiFi
  ou LoRa. Custo de peças ~R$80. Projeto escolar pode mockar com celular,
  mas vale citar o caminho na apresentação.
- **Coleira só com QR code.** Custo ~R$5. Pareada com o feature de
  sightings (#4).
- **Modo "celular como tracker".** Celular antigo amarrado na coleira usando
  a `geolocation` API do navegador. Custo zero.

## 7. Monetização — para a seção de "viabilidade"

- **Freemium.** 1 pet grátis, R$10/mês para pets ilimitados, histórico > 7
  dias e link público no modo perdido.
- **Bundle com hardware.** Coleira QR (R$30) ou coleira GPS (R$200) com o
  app incluso.
- **Parcerias com clínicas veterinárias.** Clínicas oferecem o app a clientes
  com revenue share.
- **Variante B2B.** Mesmo backend rebrandeado para abrigos (rastrear pets
  adotados) ou fazendas (rastrear gado).

---

## Plano sugerido até final de maio (MVP demonstrável)

Considerando ~4 semanas com 50% das aulas dedicadas, em ordem de prioridade:

1. **Multi-pet** ✅ (pronto)
2. **Geofence + alerta no navegador** ✅ (pronto)
3. **Modo perdido + link público** ✅ — vende o demo, ~1 sessão
4. **Página "celular sender"** (`navigator.geolocation` → POST /position) —
   transforma simulação em rastreamento real, ~1 sessão
5. **Persistência em SQLite** — sobrevive a restart, aparência profissional,
   ~1 sessão
6. **Contas de dono (cookie de sessão simples)** — habilita a #7
7. **Reportes por QR code (sighting)** — feature "uau" para a apresentação

História do produto para a apresentação:

> *Pet Tracker é um serviço que ajuda donos a encontrar pets perdidos por
> meio de rastreamento GPS, alertas de zona segura e uma rede comunitária
> de avistamentos.*

---

## Regra de processo: manter o "Como usar" atualizado

A interface tem um botão **❓ Como usar** no painel lateral que abre um modal
explicando passo a passo cada funcionalidade existente.

**Toda nova funcionalidade adicionada ao app deve incluir uma seção
correspondente nesse modal de ajuda** (`index.html`, dentro de
`#help-modal`). Sem isso, a funcionalidade não está "pronta".

Padrão de cada seção do modal:

- Título com emoji + nome curto + tag de categoria (ex.: `cadastro`,
  `geofence`, `alertas`, `visualização`).
- Lista (`<ol>` para passos sequenciais, `<ul>` para opções) com frases
  curtas no imperativo.
- Quando relevante, exemplo visual do estado/badge resultante.

Checklist ao terminar uma feature nova:

1. Implementação backend ✅
2. Implementação frontend ✅
3. Seção correspondente adicionada ao modal `❓ Como usar` ✅
4. Item marcado como ✅ no plano de MVP acima.
