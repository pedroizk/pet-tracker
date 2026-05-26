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
	"os"
	"path/filepath"
	"sort"
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

	historyLimit  = 1000
	sightingLimit = 50
	storeFile     = "store.json"
)

type Species string

const (
	SpeciesDog    Species = "dog"
	SpeciesCat    Species = "cat"
	SpeciesRabbit Species = "rabbit"
	SpeciesBird   Species = "bird"
	SpeciesOther  Species = "other"
)

func validSpecies(s Species) bool {
	switch s {
	case SpeciesDog, SpeciesCat, SpeciesRabbit, SpeciesBird, SpeciesOther:
		return true
	}
	return false
}

type SafeZone struct {
	CenterLat float64 `json:"center_lat"`
	CenterLon float64 `json:"center_lon"`
	Radius    float64 `json:"radius_meters"`
}

type Pet struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Species    Species    `json:"species"`
	Color      string     `json:"color,omitempty"`
	SafeZone   *SafeZone  `json:"safe_zone,omitempty"`
	Lost       bool       `json:"lost"`
	LostToken  string     `json:"lost_token,omitempty"`
	LostSince  *time.Time `json:"lost_since,omitempty"`
	SightToken string     `json:"sight_token,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type Position struct {
	PetID       int64     `json:"pet_id"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	Timestamp   time.Time `json:"timestamp"`
	Step        int       `json:"step"`
	OutsideZone bool      `json:"outside_zone"`
}

type Sighting struct {
	ID        string    `json:"id"`
	PetID     int64     `json:"pet_id"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Note      string    `json:"note,omitempty"`
	Reporter  string    `json:"reporter,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// StreamMessage envelopa eventos SSE.
type StreamMessage struct {
	Type     string    `json:"type"` // "position" | "alert" | "lost_status" | "sighting" | "pet_created" | "pet_removed" | "pet_updated"
	Position *Position `json:"position,omitempty"`
	Event    string    `json:"event,omitempty"`
	PetID    int64     `json:"pet_id,omitempty"`
	Pet      *Pet      `json:"pet,omitempty"`
	Sighting *Sighting `json:"sighting,omitempty"`
}

// PetTracker mantém o estado de um único pet.
type PetTracker struct {
	mu         sync.RWMutex
	pet        Pet
	zone       *SafeZone
	position   Position
	history    []Position
	sightings  []Sighting
	lost       bool
	lostToken  string
	lostSince  *time.Time
	sightToken string
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
		pet:       pet,
		position:  initial,
		history:   []Position{initial},
		sightings: []Sighting{},
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
	p.SightToken = t.sightToken
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

func (t *PetTracker) GetSightToken() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.sightToken
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

func (t *PetTracker) GetSightings() []Sighting {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Sighting, len(t.sightings))
	copy(out, t.sightings)
	return out
}

func (t *PetTracker) addSighting(s Sighting) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sightings = append(t.sightings, s)
	if len(t.sightings) > sightingLimit {
		t.sightings = t.sightings[len(t.sightings)-sightingLimit:]
	}
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

func (t *PetTracker) updateMeta(name string, species Species, color string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if name != "" {
		t.pet.Name = name
	}
	if species != "" {
		t.pet.Species = species
	}
	if color != "" {
		t.pet.Color = color
	}
}

// Stats calcula estatísticas de movimento do pet com base no histórico.
type Stats struct {
	PetID            int64     `json:"pet_id"`
	TotalDistanceM   float64   `json:"total_distance_meters"`
	StepsRecorded    int       `json:"steps_recorded"`
	FirstSeen        time.Time `json:"first_seen"`
	LastSeen         time.Time `json:"last_seen"`
	ActiveSeconds    int64     `json:"active_seconds"`
	AvgSpeedMps      float64   `json:"avg_speed_mps"`
	OutsideZoneCount int       `json:"outside_zone_count"`
}

func (t *PetTracker) ComputeStats() Stats {
	t.mu.RLock()
	hist := make([]Position, len(t.history))
	copy(hist, t.history)
	petID := t.pet.ID
	t.mu.RUnlock()

	stats := Stats{PetID: petID, StepsRecorded: len(hist)}
	if len(hist) == 0 {
		return stats
	}
	stats.FirstSeen = hist[0].Timestamp
	stats.LastSeen = hist[len(hist)-1].Timestamp
	for i := 1; i < len(hist); i++ {
		d := distanceMeters(hist[i-1].Latitude, hist[i-1].Longitude,
			hist[i].Latitude, hist[i].Longitude)
		stats.TotalDistanceM += d
	}
	for _, p := range hist {
		if p.OutsideZone {
			stats.OutsideZoneCount++
		}
	}
	stats.ActiveSeconds = int64(stats.LastSeen.Sub(stats.FirstSeen).Seconds())
	if stats.ActiveSeconds > 0 {
		stats.AvgSpeedMps = stats.TotalDistanceM / float64(stats.ActiveSeconds)
	}
	return stats
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

// Registry agrupa todos os pets e gerencia broadcast SSE + persistência.
type Registry struct {
	mu          sync.RWMutex
	nextID      int64
	pets        map[int64]*PetTracker
	clients     map[chan StreamMessage]struct{}
	lostIndex   map[string]int64 // lost_token -> pet_id
	sightIndex  map[string]int64 // sight_token -> pet_id
	storePath   string
	dirty       int32 // accessed with sync/atomic
	persistOnce sync.Once
}

func NewRegistry(storePath string) *Registry {
	return &Registry{
		pets:       make(map[int64]*PetTracker),
		clients:    make(map[chan StreamMessage]struct{}),
		lostIndex:  make(map[string]int64),
		sightIndex: make(map[string]int64),
		storePath:  storePath,
	}
}

func (r *Registry) markDirty() {
	atomic.StoreInt32(&r.dirty, 1)
}

// AddPet cria um pet com sight_token único (para QR codes anônimos).
func (r *Registry) AddPet(name string, species Species, color string) Pet {
	id := atomic.AddInt64(&r.nextID, 1)
	tok := generateToken()
	pet := Pet{
		ID:         id,
		Name:       name,
		Species:    species,
		Color:      color,
		SightToken: tok,
		CreatedAt:  time.Now(),
	}

	tracker := newPetTracker(pet)
	tracker.sightToken = tok

	r.mu.Lock()
	r.pets[id] = tracker
	r.sightIndex[tok] = id
	r.mu.Unlock()
	r.markDirty()

	snap := tracker.Snapshot()
	r.broadcast(StreamMessage{
		Type:  "pet_created",
		PetID: id,
		Pet:   &snap,
	})
	return snap
}

func (r *Registry) RemovePet(id int64) bool {
	r.mu.Lock()
	t, ok := r.pets[id]
	if !ok {
		r.mu.Unlock()
		return false
	}
	if tok := t.GetLostToken(); tok != "" {
		delete(r.lostIndex, tok)
	}
	if tok := t.GetSightToken(); tok != "" {
		delete(r.sightIndex, tok)
	}
	delete(r.pets, id)
	r.mu.Unlock()
	r.markDirty()
	r.broadcast(StreamMessage{Type: "pet_removed", PetID: id})
	return true
}

func (r *Registry) UpdatePetMeta(id int64, name, color string, species Species) (Pet, bool) {
	r.mu.RLock()
	t, ok := r.pets[id]
	r.mu.RUnlock()
	if !ok {
		return Pet{}, false
	}
	t.updateMeta(name, species, color)
	r.markDirty()
	snap := t.Snapshot()
	r.broadcast(StreamMessage{Type: "pet_updated", PetID: id, Pet: &snap})
	return snap, true
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
	r.lostIndex[token] = petID
	snap := t.Snapshot()
	r.mu.Unlock()
	r.markDirty()

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
		delete(r.lostIndex, oldToken)
	}
	snap := t.Snapshot()
	r.mu.Unlock()
	r.markDirty()

	r.broadcast(StreamMessage{
		Type:  "lost_status",
		PetID: petID,
		Pet:   &snap,
	})
	return true
}

// LookupByLostToken resolve um token público em um tracker.
func (r *Registry) LookupByLostToken(token string) (*PetTracker, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	petID, ok := r.lostIndex[token]
	if !ok {
		return nil, false
	}
	t, ok := r.pets[petID]
	return t, ok
}

func (r *Registry) LookupBySightToken(token string) (*PetTracker, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	petID, ok := r.sightIndex[token]
	if !ok {
		return nil, false
	}
	t, ok := r.pets[petID]
	return t, ok
}

func (r *Registry) AddSighting(petID int64, lat, lon float64, note, reporter string) (Sighting, bool) {
	r.mu.RLock()
	t, ok := r.pets[petID]
	r.mu.RUnlock()
	if !ok {
		return Sighting{}, false
	}
	s := Sighting{
		ID:        generateToken()[:12],
		PetID:     petID,
		Latitude:  lat,
		Longitude: lon,
		Note:      note,
		Reporter:  reporter,
		Timestamp: time.Now(),
	}
	t.addSighting(s)
	r.markDirty()
	r.broadcast(StreamMessage{Type: "sighting", PetID: petID, Sighting: &s})
	return s, true
}

func (r *Registry) ListPets() []Pet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Pet, 0, len(r.pets))
	for _, t := range r.pets {
		out = append(out, t.Snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListLostPets retorna pets em modo perdido, com posição atual e tempo perdido.
func (r *Registry) ListLostPets() []map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]map[string]interface{}, 0)
	for _, t := range r.pets {
		snap := t.Snapshot()
		if !snap.Lost {
			continue
		}
		pos := t.GetPosition()
		out = append(out, map[string]interface{}{
			"pet":      snap,
			"position": pos,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a := out[i]["pet"].(Pet).LostSince
		b := out[j]["pet"].(Pet).LostSince
		if a == nil || b == nil {
			return false
		}
		return a.After(*b)
	})
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
	r.markDirty()

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
	ch := make(chan StreamMessage, 128)
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

// ---------- Persistência em JSON ----------

type snapshotPet struct {
	Pet       Pet        `json:"pet"`
	History   []Position `json:"history"`
	Sightings []Sighting `json:"sightings"`
}

type snapshotFile struct {
	NextID int64         `json:"next_id"`
	Pets   []snapshotPet `json:"pets"`
}

// Save grava o estado atual em disco de forma atômica.
func (r *Registry) Save() error {
	if r.storePath == "" {
		return nil
	}
	r.mu.RLock()
	snap := snapshotFile{NextID: r.nextID}
	for _, t := range r.pets {
		snap.Pets = append(snap.Pets, snapshotPet{
			Pet:       t.Snapshot(),
			History:   t.GetHistory(),
			Sightings: t.GetSightings(),
		})
	}
	r.mu.RUnlock()

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.storePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, r.storePath); err != nil {
		return err
	}
	atomic.StoreInt32(&r.dirty, 0)
	return nil
}

// Load lê o estado de disco (se existir).
func (r *Registry) Load() error {
	if r.storePath == "" {
		return nil
	}
	data, err := os.ReadFile(r.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var snap snapshotFile
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID = snap.NextID
	for _, sp := range snap.Pets {
		pet := sp.Pet
		tracker := &PetTracker{
			pet:       pet,
			history:   sp.History,
			sightings: sp.Sightings,
		}
		if pet.SafeZone != nil {
			z := *pet.SafeZone
			tracker.zone = &z
		}
		if len(sp.History) > 0 {
			tracker.position = sp.History[len(sp.History)-1]
		} else {
			tracker.position = Position{
				PetID:     pet.ID,
				Latitude:  startLat,
				Longitude: startLon,
				Timestamp: time.Now(),
				Step:      0,
			}
			tracker.history = []Position{tracker.position}
		}
		if pet.Lost {
			tracker.lost = true
			tracker.lostToken = pet.LostToken
			tracker.lostSince = pet.LostSince
			if pet.LostToken != "" {
				r.lostIndex[pet.LostToken] = pet.ID
			}
		}
		if pet.SightToken != "" {
			tracker.sightToken = pet.SightToken
			r.sightIndex[pet.SightToken] = pet.ID
		}
		r.pets[pet.ID] = tracker
	}
	return nil
}

// StartAutoPersist persiste a cada 2s se houve mudança.
func (r *Registry) StartAutoPersist() {
	r.persistOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				if atomic.LoadInt32(&r.dirty) == 1 {
					if err := r.Save(); err != nil {
						log.Printf("persist error: %v", err)
					}
				}
			}
		}()
	})
}

// ---------- Geometria ----------

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
// Pets em modo perdido continuam se movendo (mais devagar), simulando que
// estão andando por aí — útil para a demo do mural.
func startSimulation(reg *Registry) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		time.Sleep(time.Duration(2+rng.Float64()*2) * time.Second)

		for _, t := range reg.AllTrackers() {
			current := t.GetPosition()
			bearing := rng.Float64() * 2 * math.Pi
			step := stepMeters
			snap := t.Snapshot()
			if snap.Lost {
				step = stepMeters * 0.5
			}
			newLat, newLon := movePoint(current.Latitude, current.Longitude, step, bearing)
			reg.UpdatePosition(snap.ID, newLat, newLon)
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
				Color   string  `json:"color"`
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
			if len(body.Name) > 60 {
				http.Error(w, "name muito longo (máx 60)", http.StatusBadRequest)
				return
			}
			if !validSpecies(body.Species) {
				http.Error(w, "species inválida", http.StatusBadRequest)
				return
			}
			pet := reg.AddPet(body.Name, body.Species, body.Color)
			writeJSON(w, http.StatusCreated, pet)
		default:
			http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
		}
	}
}

// petItemHandler trata /pets/{id} e /pets/{id}/...
func petItemHandler(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/pets/")
		parts := strings.Split(path, "/")
		if len(parts) == 0 || parts[0] == "" {
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

		if len(parts) == 1 {
			handlePetRoot(w, r, reg, id, t)
			return
		}

		switch parts[1] {
		case "zone":
			handleZone(w, r, reg, t)
		case "lost":
			handleLost(w, r, reg, id)
		case "stats":
			handleStats(w, r, t)
		case "sightings":
			handleSightings(w, r, t)
		case "qr":
			handlePetQR(w, r, t)
		default:
			http.Error(w, "rota inválida", http.StatusNotFound)
		}
	}
}

func handlePetRoot(w http.ResponseWriter, r *http.Request, reg *Registry, id int64, t *PetTracker) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, t.Snapshot())
	case http.MethodPatch:
		var body struct {
			Name    string  `json:"name"`
			Species Species `json:"species"`
			Color   string  `json:"color"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		if len(body.Name) > 60 {
			http.Error(w, "name muito longo", http.StatusBadRequest)
			return
		}
		if body.Species != "" && !validSpecies(body.Species) {
			http.Error(w, "species inválida", http.StatusBadRequest)
			return
		}
		updated, ok := reg.UpdatePetMeta(id, body.Name, body.Color, body.Species)
		if !ok {
			http.Error(w, "pet não encontrado", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if !reg.RemovePet(id) {
			http.Error(w, "pet não encontrado", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
	}
}

func handleStats(w http.ResponseWriter, r *http.Request, t *PetTracker) {
	if r.Method != http.MethodGet {
		http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, t.ComputeStats())
}

func handleSightings(w http.ResponseWriter, r *http.Request, t *PetTracker) {
	if r.Method != http.MethodGet {
		http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, t.GetSightings())
}

func handlePetQR(w http.ResponseWriter, r *http.Request, t *PetTracker) {
	if r.Method != http.MethodGet {
		http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
		return
	}
	snap := t.Snapshot()
	if snap.SightToken == "" {
		http.Error(w, "pet sem sight token", http.StatusNotFound)
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sight_token": snap.SightToken,
		"sight_url":   fmt.Sprintf("%s://%s/sight/%s", scheme, r.Host, snap.SightToken),
		"pet":         snap,
	})
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
		t, ok := reg.LookupByLostToken(token)
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
			if len(history) > 200 {
				history = history[len(history)-200:]
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"pet":       snap,
				"position":  t.GetPosition(),
				"history":   history,
				"sightings": t.GetSightings(),
			})
		default:
			http.NotFound(w, r)
		}
	}
}

// sightHandler trata as URLs públicas /sight/{token} (QR code).
func sightHandler(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/sight/")
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
		t, ok := reg.LookupBySightToken(token)
		if !ok {
			http.NotFound(w, r)
			return
		}

		switch {
		case len(parts) == 1:
			switch r.Method {
			case http.MethodGet:
				http.ServeFile(w, r, "sight.html")
			case http.MethodPost:
				var body struct {
					Latitude  float64 `json:"latitude"`
					Longitude float64 `json:"longitude"`
					Note      string  `json:"note"`
					Reporter  string  `json:"reporter"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "JSON inválido", http.StatusBadRequest)
					return
				}
				if body.Latitude < -90 || body.Latitude > 90 ||
					body.Longitude < -180 || body.Longitude > 180 {
					http.Error(w, "coordenadas fora do intervalo", http.StatusBadRequest)
					return
				}
				if len(body.Note) > 280 {
					body.Note = body.Note[:280]
				}
				if len(body.Reporter) > 60 {
					body.Reporter = body.Reporter[:60]
				}
				s, ok := reg.AddSighting(t.Snapshot().ID, body.Latitude, body.Longitude, body.Note, body.Reporter)
				if !ok {
					http.Error(w, "pet não encontrado", http.StatusNotFound)
					return
				}
				writeJSON(w, http.StatusCreated, s)
			default:
				http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
			}
		case len(parts) == 2 && parts[1] == "state":
			snap := t.Snapshot()
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"pet":       snap,
				"position":  t.GetPosition(),
				"sightings": t.GetSightings(),
			})
		default:
			http.NotFound(w, r)
		}
	}
}

func muralHandler(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, reg.ListLostPets())
	}
}

func handleZone(w http.ResponseWriter, r *http.Request, reg *Registry, t *PetTracker) {
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
		reg.markDirty()
		writeJSON(w, http.StatusOK, z)
	case http.MethodDelete:
		t.SetZone(nil)
		reg.markDirty()
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
				out[t.Snapshot().ID] = t.GetHistory()
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

		// heartbeat a cada 15s (mantém proxies vivos)
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				fmt.Fprintf(w, ": ping %d\n\n", time.Now().Unix())
				flusher.Flush()
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

func healthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"version": "2.0.0",
		"time":    time.Now().Format(time.RFC3339),
	})
}

// staticPage envia um arquivo HTML do diretório raiz.
func staticPage(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Clean(name))
	}
}

func main() {
	storePath := storeFile
	if v := os.Getenv("PET_TRACKER_STORE"); v != "" {
		storePath = v
	}

	reg := NewRegistry(storePath)
	if err := reg.Load(); err != nil {
		log.Printf("warn: falha ao carregar %s: %v", storePath, err)
	}

	// Se não havia nada salvo, cria dois pets de exemplo.
	if len(reg.ListPets()) == 0 {
		reg.AddPet("Rei Julian", SpeciesDog, "#c0392b")
		reg.AddPet("Mia", SpeciesCat, "#2980b9")
	}

	reg.StartAutoPersist()
	go startSimulation(reg)

	http.HandleFunc("/pets", petsHandler(reg))
	http.HandleFunc("/pets/", petItemHandler(reg))
	http.HandleFunc("/position", positionHandler(reg))
	http.HandleFunc("/history", historyHandler(reg))
	http.HandleFunc("/stream", sseHandler(reg))
	http.HandleFunc("/lost/", lostHandler(reg))
	http.HandleFunc("/sight/", sightHandler(reg))
	http.HandleFunc("/api/mural", muralHandler(reg))
	http.HandleFunc("/healthz", healthHandler)

	http.HandleFunc("/sender", staticPage("sender.html"))
	http.HandleFunc("/mural", staticPage("mural.html"))

	fs := http.FileServer(http.Dir("."))
	http.Handle("/", fs)

	addr := ":8080"
	if v := os.Getenv("PET_TRACKER_ADDR"); v != "" {
		addr = v
	}

	log.Printf("Pet Tracker em http://localhost%s", addr)
	log.Println("GET  /pets                  -> lista pets")
	log.Println("POST /pets                  -> cria pet {name, species, color?}")
	log.Println("GET  /pets/{id}             -> detalhes do pet")
	log.Println("PATCH /pets/{id}            -> renomeia/edita pet")
	log.Println("DEL  /pets/{id}             -> remove pet")
	log.Println("GET  /pets/{id}/stats       -> estatísticas de movimento")
	log.Println("GET  /pets/{id}/sightings   -> lista avistamentos")
	log.Println("GET  /pets/{id}/qr          -> sight_url p/ QR code")
	log.Println("PUT  /pets/{id}/zone        -> define zona segura")
	log.Println("DEL  /pets/{id}/zone        -> remove zona")
	log.Println("POST /pets/{id}/lost        -> marca como perdido")
	log.Println("DEL  /pets/{id}/lost        -> desmarca perdido")
	log.Println("GET  /position              -> posição atual de todos")
	log.Println("POST /position              -> ingere posição {pet_id, latitude, longitude}")
	log.Println("GET  /history?pet_id=1      -> histórico do pet")
	log.Println("GET  /stream                -> stream SSE")
	log.Println("GET  /lost/{token}          -> página pública de pet perdido")
	log.Println("GET  /lost/{token}/state    -> estado JSON")
	log.Println("GET  /sight/{token}         -> página pública de avistamento (QR)")
	log.Println("POST /sight/{token}         -> reporta avistamento")
	log.Println("GET  /api/mural             -> lista pública de pets perdidos")
	log.Println("GET  /mural                 -> página mural público")
	log.Println("GET  /sender                -> celular como tracker")
	log.Println("GET  /healthz               -> health check")
	log.Fatal(http.ListenAndServe(addr, nil))
}
