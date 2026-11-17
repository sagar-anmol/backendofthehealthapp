package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/sagar-anmol/backendofthehealthapp/internal/auth"
	"github.com/sagar-anmol/backendofthehealthapp/internal/clubber"
	"github.com/sagar-anmol/backendofthehealthapp/internal/db"
	"github.com/sagar-anmol/backendofthehealthapp/internal/omniroute"
)

type Server struct {
	db        *db.DB
	clubber   *clubber.Clubber
	router    *omniroute.Router
	startTime time.Time
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "hosti.db"
	}

	primaryProvider := os.Getenv("PRIMARY_PROVIDER")
	if primaryProvider == "" {
		primaryProvider = "gemini"
	}

	geminiKey := os.Getenv("GEMINI_API_KEY")
	groqKey := os.Getenv("GROQ_API_KEY")
	openRouterKey := os.Getenv("OPENROUTER_API_KEY")

	// Initialize Database
	database, err := db.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	// Initialize OmniRoute
	router := omniroute.NewRouter(primaryProvider, geminiKey, groqKey, openRouterKey)

	// Initialize Prompt Clubber (200ms batching window, up to 8 requests per batch)
	club := clubber.NewClubber(database, router, 8, 200*time.Millisecond)

	s := &Server{
		db:        database,
		clubber:   club,
		router:    router,
		startTime: time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/chat", s.handleChat)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /", s.handleRoot)

	addr := ":" + port
	log.Printf("=========================================================")
	log.Printf(" Hosti Backend (Go + gVisor Sandbox) listening on %s", addr)
	log.Printf(" Primary Provider: %s", primaryProvider)
	log.Printf(" Database: %s", dbPath)
	log.Printf(" Prompt Clubber Window: 200ms (Max Batch: 8)")
	log.Printf("=========================================================")

	if err := http.ListenAndServe(addr, corsMiddleware(mux)); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" || body.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request. Provide username and password"})
		return
	}

	user, err := s.db.GetUserByUsername(body.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error"})
		return
	}

	if user == nil {
		// Auto-register new user
		hash, err := auth.HashPassword(body.Password)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Hash error"})
			return
		}
		token := auth.GenerateToken()
		newUser, err := s.db.CreateUser(body.Username, hash, token)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "User creation error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":   "ok",
			"message":  "User registered and authenticated",
			"username": newUser.Username,
			"token":    newUser.Token,
		})
		return
	}

	// Verify existing password
	if !auth.CheckPassword(user.PasswordHash, body.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid password"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "ok",
		"message":  "Authenticated successfully",
		"username": user.Username,
		"token":    user.Token,
	})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Missing or invalid Authorization header"})
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")

	user, err := s.db.GetUserByToken(token)
	if err != nil || user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid or expired token"})
		return
	}

	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Text field cannot be empty"})
		return
	}

	// Fetch user's persistent long-term memories from SQLite
	memories, _ := s.db.GetMemories(user.ID)

	// Submit to Prompt Clubber
	respChan := s.clubber.Submit(clubber.UserRequest{
		UserID:   user.ID,
		Username: user.Username,
		Text:     body.Text,
		Memories: memories,
	})

	// Wait for demultiplexed isolated response
	res := <-respChan
	if res.Error != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Upstream error: " + res.Error.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"reply":          res.Reply,
		"memories_saved": res.NewMemories,
		"provider":       res.Provider,
		"batched_with":   res.BatchedWith,
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	ramMB := float64(m.Alloc) / 1024 / 1024

	totalReqs, batchedCalls, upstreamCalls := s.clubber.GetStats()
	callsSaved := batchedCalls - upstreamCalls
	if callsSaved < 0 {
		callsSaved = 0
	}

	uptimeSec := int64(time.Since(s.startTime).Seconds())

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":               "ok",
		"service":              "hosti-backend",
		"runtime":              "go",
		"uptime_seconds":       uptimeSec,
		"ram_usage_mb":         fmt.Sprintf("%.2f MB", ramMB),
		"goroutines":           runtime.NumGoroutine(),
		"total_requests":       totalReqs,
		"batched_requests":     batchedCalls,
		"upstream_calls_saved": callsSaved,
	})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.handleHealth(w, r)
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
