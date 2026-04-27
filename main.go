package main

import (
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	earthRadiusMeters = 6371000.0
	stepMeters        = 10.0

	// Praça Coronel Pedro Osório, Pelotas - RS
	startLat = -31.770687426923516
	startLon = -52.34135057529372

	historyLimit = 1000
)

type Species string

const (
	SpeciesDog Species = "dog"
	SpeciesCat Species = "cat"
)

type SafeZone struct {
	CenterLat float64 `json:"center_lat"`
	CenterLon float64 `json:"center_lon"`
	Radius    float64 `json:"radius_meters"`
}

type Pet struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Species   Species    `json:"species"`
	SafeZone  *SafeZone  `json:"safe_zone,omitempty"`
	Lost      bool       `json:"lost"`
	LostToken string     `json:"lost_token,omitempty"`
	LostSince *time.Time `json:"lost_since,omitempty"`
}

type Position struct {
	PetID       int64     `json:"pet_id"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	Timestamp   time.Time `json:"timestamp"`
	Step        int       `json:"step"`
	OutsideZone bool      `json:"outside_zone"`
}

// StreamMessage envelopa eventos SSE.
type StreamMessage struct {
	Type     string    `json:"type"` // "position" | "alert" | "lost_status"
	Position *Position `json:"position,omitempty"`
	Event    string    `json:"event,omitempty"` // "left_zone" | "returned_zone"
	PetID    int64     `json:"pet_id,omitempty"`
	Pet      *Pet      `json:"pet,omitempty"` // usado em "lost_status"
}

// PetTracker mantém o estado de um único pet.
type PetTracker struct {
	mu        sync.RWMutex
	pet       Pet
	zone      *SafeZone
	position  Position
	history   []Position
	lost      bool
	lostToken string
	lostSince *time.Time
}

func newPetTracker(pet Pet) *PetTracker {
	initial := Position{
		PetID:     pet.ID,
		Latitude:  startLat,
		Longitude: startLon,
		Timestamp: time.Now(),
		Step:      0,
	}
	return &PetTracker{
		pet:      pet,
		position: initial,
		history:  []Position{initial},
	}
}

func (t *PetTracker) Snapshot() Pet {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p := t.pet
	if t.zone != nil {
		z := *t.zone
		p.SafeZone = &z
	}
	p.Lost = t.lost
	p.LostToken = t.lostToken
	if t.lostSince != nil {
		ts := *t.lostSince
		p.LostSince = &ts
	}
	return p
}

func (t *PetTracker) GetLostToken() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lostToken
}

func (t *PetTracker) setLost(token string, since time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lost = true
	t.lostToken = token
	t.lostSince = &since
}

func (t *PetTracker) clearLostState() (oldToken string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	oldToken = t.lostToken
	t.lost = false
	t.lostToken = ""
	t.lostSince = nil
	return
}

func (t *PetTracker) GetPosition() Position {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.position
}

func (t *PetTracker) GetHistory() []Position {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Position, len(t.history))
	copy(out, t.history)
	return out
}

func (t *PetTracker) GetZone() *SafeZone {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.zone == nil {
		return nil
	}
	z := *t.zone
	return &z
}

func (t *PetTracker) SetZone(z *SafeZone) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.zone = z
}

// applyPosition grava a nova posição, calcula OutsideZone e devolve a posição
// anterior (para detecção de transição) e o flag anterior.
func (t *PetTracker) applyPosition(p Position) (prev Position, prevOutside bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	prev = t.position
	prevOutside = prev.OutsideZone

	if t.zone != nil {
		d := distanceMeters(p.Latitude, p.Longitude, t.zone.CenterLat, t.zone.CenterLon)
		p.OutsideZone = d > t.zone.Radius
	} else {
		p.OutsideZone = false
	}

	t.position = p
	t.history = append(t.history, p)
	if len(t.history) > historyLimit {
		t.history = t.history[len(t.history)-historyLimit:]
	}
	return prev, prevOutside
}

// Registry agrupa todos os pets e gerencia broadcast SSE.
type Registry struct {
	mu         sync.RWMutex
	nextID     int64
	pets       map[int64]*PetTracker
	clients    map[chan StreamMessage]struct{}
	tokenIndex map[string]int64 // lost_token -> pet_id
}

func NewRegistry() *Registry {
	return &Registry{
		pets:       make(map[int64]*PetTracker),
		clients:    make(map[chan StreamMessage]struct{}),
		tokenIndex: make(map[string]int64),
	}
}

func (r *Registry) AddPet(name string, species Species) Pet {
	id := atomic.AddInt64(&r.nextID, 1)
	pet := Pet{ID: id, Name: name, Species: species}

	r.mu.Lock()
	r.pets[id] = newPetTracker(pet)
	r.mu.Unlock()

	return pet
}

func (r *Registry) RemovePet(id int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.pets[id]
	if !ok {
		return false
	}
	if tok := t.GetLostToken(); tok != "" {
		delete(r.tokenIndex, tok)
	}
	delete(r.pets, id)
	return true
}

func generateToken() string {
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// MarkLost gera um token público para o pet e dispara um evento SSE.
// Idempotente: se o pet já está perdido, devolve o token existente.
func (r *Registry) MarkLost(petID int64) (string, bool) {
	r.mu.Lock()
	t, ok := r.pets[petID]
	if !ok {
		r.mu.Unlock()
		return "", false
	}
	if existing := t.GetLostToken(); existing != "" {
		r.mu.Unlock()
		return existing, true
	}
	token := generateToken()
	if token == "" {
		r.mu.Unlock()
		return "", false
	}
	t.setLost(token, time.Now())
	r.tokenIndex[token] = petID
	snap := t.Snapshot()
	r.mu.Unlock()

	r.broadcast(StreamMessage{
		Type:  "lost_status",
		PetID: petID,
		Pet:   &snap,
	})
	return token, true
}

// ClearLost remove o estado de perdido e invalida o token público.
func (r *Registry) ClearLost(petID int64) bool {
	r.mu.Lock()
	t, ok := r.pets[petID]
	if !ok {
		r.mu.Unlock()
		return false
	}
	oldToken := t.clearLostState()
	if oldToken != "" {
		delete(r.tokenIndex, oldToken)
	}
	snap := t.Snapshot()
	r.mu.Unlock()

	r.broadcast(StreamMessage{
		Type:  "lost_status",
		PetID: petID,
		Pet:   &snap,
	})
	return true
}

// LookupByToken resolve um token público em um tracker.
func (r *Registry) LookupByToken(token string) (*PetTracker, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	petID, ok := r.tokenIndex[token]
	if !ok {
		return nil, false
	}
	t, ok := r.pets[petID]
	return t, ok
}

func (r *Registry) ListPets() []Pet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Pet, 0, len(r.pets))
	for _, t := range r.pets {
		out = append(out, t.Snapshot())
	}
	return out
}

func (r *Registry) GetTracker(id int64) (*PetTracker, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.pets[id]
	return t, ok
}

func (r *Registry) AllTrackers() []*PetTracker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*PetTracker, 0, len(r.pets))
	for _, t := range r.pets {
		out = append(out, t)
	}
	return out
}

func (r *Registry) UpdatePosition(petID int64, lat, lon float64) (Position, bool) {
	r.mu.RLock()
	t, ok := r.pets[petID]
	r.mu.RUnlock()
	if !ok {
		return Position{}, false
	}

	current := t.GetPosition()
	candidate := Position{
		PetID:     petID,
		Latitude:  lat,
		Longitude: lon,
		Timestamp: time.Now(),
		Step:      current.Step + 1,
	}

	_, prevOutside := t.applyPosition(candidate)
	stored := t.GetPosition()

	r.broadcast(StreamMessage{
		Type:     "position",
		Position: &stored,
	})

	switch {
	case !prevOutside && stored.OutsideZone:
		r.broadcast(StreamMessage{
			Type:     "alert",
			Event:    "left_zone",
			PetID:    petID,
			Position: &stored,
		})
		log.Printf("ALERT pet=%d (%s) saiu da zona segura", petID, t.Snapshot().Name)
	case prevOutside && !stored.OutsideZone:
		r.broadcast(StreamMessage{
			Type:     "alert",
			Event:    "returned_zone",
			PetID:    petID,
			Position: &stored,
		})
		log.Printf("INFO  pet=%d (%s) voltou à zona segura", petID, t.Snapshot().Name)
	}

	return stored, true
}

func (r *Registry) Subscribe() chan StreamMessage {
	ch := make(chan StreamMessage, 64)
	r.mu.Lock()
	r.clients[ch] = struct{}{}
	r.mu.Unlock()
	return ch
}

func (r *Registry) Unsubscribe(ch chan StreamMessage) {
	r.mu.Lock()
	delete(r.clients, ch)
	r.mu.Unlock()
	close(ch)
}

func (r *Registry) broadcast(m StreamMessage) {
	r.mu.RLock()
	clients := make([]chan StreamMessage, 0, len(r.clients))
	for ch := range r.clients {
		clients = append(clients, ch)
	}
	r.mu.RUnlock()

	for _, ch := range clients {
		select {
		case ch <- m:
		default:
		}
	}
}

// distanceMeters calcula a distância haversine entre dois pontos em metros.
func distanceMeters(lat1, lon1, lat2, lon2 float64) float64 {
	phi1 := degreesToRadians(lat1)
	phi2 := degreesToRadians(lat2)
	dPhi := degreesToRadians(lat2 - lat1)
	dLambda := degreesToRadians(lon2 - lon1)

	a := math.Sin(dPhi/2)*math.Sin(dPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*
			math.Sin(dLambda/2)*math.Sin(dLambda/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusMeters * c
}

// movePoint desloca um ponto geográfico por certa distância em uma direção dada.
func movePoint(latDeg, lonDeg, distMeters, bearingRad float64) (float64, float64) {
	lat1 := degreesToRadians(latDeg)
	lon1 := degreesToRadians(lonDeg)
	angularDistance := distMeters / earthRadiusMeters

	lat2 := math.Asin(
		math.Sin(lat1)*math.Cos(angularDistance) +
			math.Cos(lat1)*math.Sin(angularDistance)*math.Cos(bearingRad),
	)

	lon2 := lon1 + math.Atan2(
		math.Sin(bearingRad)*math.Sin(angularDistance)*math.Cos(lat1),
		math.Cos(angularDistance)-math.Sin(lat1)*math.Sin(lat2),
	)

	return radiansToDegrees(lat2), normalizeLongitude(radiansToDegrees(lon2))
}

func degreesToRadians(d float64) float64 { return d * math.Pi / 180.0 }
func radiansToDegrees(r float64) float64 { return r * 180.0 / math.Pi }

func normalizeLongitude(lon float64) float64 {
	for lon > 180.0 {
		lon -= 360.0
	}
	for lon < -180.0 {
		lon += 360.0
	}
	return lon
}

// startSimulation move todos os pets registrados em passos aleatórios.
func startSimulation(reg *Registry) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		time.Sleep(time.Duration(2+rng.Float64()*2) * time.Second)

		for _, t := range reg.AllTrackers() {
			current := t.GetPosition()
			bearing := rng.Float64() * 2 * math.Pi
			newLat, newLon := movePoint(current.Latitude, current.Longitude, stepMeters, bearing)
			reg.UpdatePosition(t.pet.ID, newLat, newLon)
		}
	}
}

// ---------- Handlers ----------

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func petsHandler(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, reg.ListPets())
		case http.MethodPost:
			var body struct {
				Name    string  `json:"name"`
				Species Species `json:"species"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "JSON inválido", http.StatusBadRequest)
				return
			}
			body.Name = strings.TrimSpace(body.Name)
			if body.Name == "" {
				http.Error(w, "name obrigatório", http.StatusBadRequest)
				return
			}
			if body.Species != SpeciesDog && body.Species != SpeciesCat {
				http.Error(w, "species deve ser 'dog' ou 'cat'", http.StatusBadRequest)
				return
			}
			pet := reg.AddPet(body.Name, body.Species)
			writeJSON(w, http.StatusCreated, pet)
		default:
			http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
		}
	}
}

// petItemHandler trata caminhos /pets/{id}/... — atualmente apenas /pets/{id}/zone.
func petItemHandler(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/pets/")
		parts := strings.Split(path, "/")
		if len(parts) < 2 || parts[0] == "" {
			http.Error(w, "rota inválida", http.StatusNotFound)
			return
		}
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			http.Error(w, "id inválido", http.StatusBadRequest)
			return
		}
		t, ok := reg.GetTracker(id)
		if !ok {
			http.Error(w, "pet não encontrado", http.StatusNotFound)
			return
		}

		switch parts[1] {
		case "zone":
			handleZone(w, r, t)
		case "lost":
			handleLost(w, r, reg, id)
		default:
			http.Error(w, "rota inválida", http.StatusNotFound)
		}
	}
}

func handleLost(w http.ResponseWriter, r *http.Request, reg *Registry, petID int64) {
	switch r.Method {
	case http.MethodPost:
		token, ok := reg.MarkLost(petID)
		if !ok {
			http.Error(w, "pet não encontrado", http.StatusNotFound)
			return
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"lost":       true,
			"lost_token": token,
			"public_url": fmt.Sprintf("%s://%s/lost/%s", scheme, r.Host, token),
		})
	case http.MethodDelete:
		if !reg.ClearLost(petID) {
			http.Error(w, "pet não encontrado", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
	}
}

func validHexToken(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// lostHandler trata as URLs públicas /lost/{token} e /lost/{token}/state.
func lostHandler(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/lost/")
		parts := strings.Split(path, "/")
		if len(parts) == 0 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		token := parts[0]
		if !validHexToken(token) {
			http.NotFound(w, r)
			return
		}
		t, ok := reg.LookupByToken(token)
		if !ok {
			http.NotFound(w, r)
			return
		}

		switch {
		case len(parts) == 1:
			http.ServeFile(w, r, "lost.html")
		case len(parts) == 2 && parts[1] == "state":
			snap := t.Snapshot()
			history := t.GetHistory()
			if len(history) > 100 {
				history = history[len(history)-100:]
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"pet":      snap,
				"position": t.GetPosition(),
				"history":  history,
			})
		default:
			http.NotFound(w, r)
		}
	}
}

func handleZone(w http.ResponseWriter, r *http.Request, t *PetTracker) {
	switch r.Method {
	case http.MethodGet:
		z := t.GetZone()
		if z == nil {
			writeJSON(w, http.StatusOK, nil)
			return
		}
		writeJSON(w, http.StatusOK, z)
	case http.MethodPut:
		var z SafeZone
		if err := json.NewDecoder(r.Body).Decode(&z); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}
		if z.Radius <= 0 {
			http.Error(w, "radius_meters deve ser > 0", http.StatusBadRequest)
			return
		}
		if z.CenterLat < -90 || z.CenterLat > 90 || z.CenterLon < -180 || z.CenterLon > 180 {
			http.Error(w, "coordenadas fora do intervalo", http.StatusBadRequest)
			return
		}
		t.SetZone(&z)
		writeJSON(w, http.StatusOK, z)
	case http.MethodDelete:
		t.SetZone(nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
	}
}

func positionHandler(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			trackers := reg.AllTrackers()
			out := make([]Position, 0, len(trackers))
			for _, t := range trackers {
				out = append(out, t.GetPosition())
			}
			writeJSON(w, http.StatusOK, out)
		case http.MethodPost:
			var body struct {
				PetID     int64   `json:"pet_id"`
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "JSON inválido", http.StatusBadRequest)
				return
			}
			pos, ok := reg.UpdatePosition(body.PetID, body.Latitude, body.Longitude)
			if !ok {
				http.Error(w, "pet não encontrado", http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, pos)
		default:
			http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
		}
	}
}

func historyHandler(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.URL.Query().Get("pet_id")
		if idStr == "" {
			out := make(map[int64][]Position)
			for _, t := range reg.AllTrackers() {
				out[t.pet.ID] = t.GetHistory()
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "pet_id inválido", http.StatusBadRequest)
			return
		}
		t, ok := reg.GetTracker(id)
		if !ok {
			http.Error(w, "pet não encontrado", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, t.GetHistory())
	}
}

func sseHandler(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming não suportado", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("X-Accel-Buffering", "no")

		ch := reg.Subscribe()
		defer reg.Unsubscribe(ch)

		// snapshot inicial: posição atual de cada pet já registrado
		for _, t := range reg.AllTrackers() {
			pos := t.GetPosition()
			payload, _ := json.Marshal(StreamMessage{
				Type:     "position",
				Position: &pos,
			})
			fmt.Fprintf(w, "data: %s\n\n", payload)
		}
		flusher.Flush()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-ch:
				payload, err := json.Marshal(msg)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}
	}
}

func main() {
	reg := NewRegistry()

	reg.AddPet("Rei Julian", SpeciesDog)
	reg.AddPet("Mia", SpeciesCat)

	go startSimulation(reg)

	http.HandleFunc("/pets", petsHandler(reg))
	http.HandleFunc("/pets/", petItemHandler(reg))
	http.HandleFunc("/position", positionHandler(reg))
	http.HandleFunc("/history", historyHandler(reg))
	http.HandleFunc("/stream", sseHandler(reg))
	http.HandleFunc("/lost/", lostHandler(reg))

	fs := http.FileServer(http.Dir("."))
	http.Handle("/", fs)

	log.Println("Pet Tracker em http://localhost:8080")
	log.Println("GET  /pets                -> lista pets")
	log.Println("POST /pets                -> cria pet {name, species}")
	log.Println("PUT  /pets/{id}/zone      -> define zona {center_lat, center_lon, radius_meters}")
	log.Println("DEL  /pets/{id}/zone      -> remove zona")
	log.Println("GET  /position            -> posição atual de todos os pets")
	log.Println("POST /position            -> ingere posição {pet_id, latitude, longitude}")
	log.Println("GET  /history?pet_id=1    -> histórico de um pet")
	log.Println("GET  /stream              -> stream SSE com atualizações + alertas")
	log.Println("POST /pets/{id}/lost      -> marca pet como perdido, devolve link público")
	log.Println("DEL  /pets/{id}/lost      -> desmarca pet como perdido")
	log.Println("GET  /lost/{token}        -> página pública de pet perdido")
	log.Println("GET  /lost/{token}/state  -> estado JSON do pet perdido")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
