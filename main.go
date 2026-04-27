package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
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

type Pet struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	Species Species `json:"species"`
}

type Position struct {
	PetID     int64     `json:"pet_id"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Timestamp time.Time `json:"timestamp"`
	Step      int       `json:"step"`
}

// PetTracker mantém o estado de um único pet.
type PetTracker struct {
	mu       sync.RWMutex
	pet      Pet
	position Position
	history  []Position
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

func (t *PetTracker) SetPosition(p Position) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.position = p
	t.history = append(t.history, p)
	if len(t.history) > historyLimit {
		t.history = t.history[len(t.history)-historyLimit:]
	}
}

// Registry agrupa todos os pets e gerencia broadcast SSE.
type Registry struct {
	mu      sync.RWMutex
	nextID  int64
	pets    map[int64]*PetTracker
	clients map[chan Position]struct{}
}

func NewRegistry() *Registry {
	return &Registry{
		pets:    make(map[int64]*PetTracker),
		clients: make(map[chan Position]struct{}),
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
	if _, ok := r.pets[id]; !ok {
		return false
	}
	delete(r.pets, id)
	return true
}

func (r *Registry) ListPets() []Pet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Pet, 0, len(r.pets))
	for _, t := range r.pets {
		out = append(out, t.pet)
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
	next := Position{
		PetID:     petID,
		Latitude:  lat,
		Longitude: lon,
		Timestamp: time.Now(),
		Step:      current.Step + 1,
	}
	t.SetPosition(next)
	r.broadcast(next)
	return next, true
}

func (r *Registry) Subscribe() chan Position {
	ch := make(chan Position, 32)
	r.mu.Lock()
	r.clients[ch] = struct{}{}
	r.mu.Unlock()
	return ch
}

func (r *Registry) Unsubscribe(ch chan Position) {
	r.mu.Lock()
	delete(r.clients, ch)
	r.mu.Unlock()
	close(ch)
}

func (r *Registry) broadcast(p Position) {
	r.mu.RLock()
	clients := make([]chan Position, 0, len(r.clients))
	for ch := range r.clients {
		clients = append(clients, ch)
	}
	r.mu.RUnlock()

	for _, ch := range clients {
		select {
		case ch <- p:
		default:
		}
	}
}

// movePoint desloca um ponto geográfico por certa distância em uma direção dada.
func movePoint(latDeg, lonDeg, distanceMeters, bearingRad float64) (float64, float64) {
	lat1 := degreesToRadians(latDeg)
	lon1 := degreesToRadians(lonDeg)
	angularDistance := distanceMeters / earthRadiusMeters

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
// Útil para demonstração enquanto não há dispositivo GPS real.
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
		var id int64
		if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
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

		for _, t := range reg.AllTrackers() {
			pos := t.GetPosition()
			payload, _ := json.Marshal(pos)
			fmt.Fprintf(w, "data: %s\n\n", payload)
		}
		flusher.Flush()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case pos := <-ch:
				payload, err := json.Marshal(pos)
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

	// pets iniciais para demo
	reg.AddPet("Rei Julian", SpeciesDog)
	reg.AddPet("Mia", SpeciesCat)

	go startSimulation(reg)

	http.HandleFunc("/pets", petsHandler(reg))
	http.HandleFunc("/position", positionHandler(reg))
	http.HandleFunc("/history", historyHandler(reg))
	http.HandleFunc("/stream", sseHandler(reg))

	fs := http.FileServer(http.Dir("."))
	http.Handle("/", fs)

	log.Println("Pet Tracker em http://localhost:8080")
	log.Println("GET  /pets             -> lista pets")
	log.Println("POST /pets             -> cria pet {name, species}")
	log.Println("GET  /position         -> posição atual de todos os pets")
	log.Println("POST /position         -> ingere posição {pet_id, latitude, longitude}")
	log.Println("GET  /history?pet_id=1 -> histórico de um pet")
	log.Println("GET  /stream           -> stream SSE com atualizações")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
