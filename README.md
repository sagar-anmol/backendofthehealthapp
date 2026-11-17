# Hosti Backend (`backendofthehealthapp`)

Ultra-lightweight (~10MB RAM) Go backend service running inside Google gVisor (`runsc`) sandbox with prompt clubbing and autonomous long-term memory.

## Architecture Highlights
- **Ultra-low memory footprint:** Consumes only ~10MB RSS.
- **Prompt Clubbing / Micro-Batching:** Gathers concurrent user requests in a 200ms window into a single upstream LLM call.
- **Cryptographic Envelope Isolation:** Every request is tagged with random nonces (`REQ_<hex>`) and delimiter-escaped to prevent prompt injection or cross-user leakage.
- **Embedded SQLite:** Pure-Go database (`modernc.org/sqlite`) for persistent user tokens and long-term health memories.
- **OmniRoute Gateway:** Supports Gemini, Groq, OpenRouter, and an offline mock engine.

---

## API Endpoints

### 1. User Login / Registration
- **Method:** `POST /auth/login`
- **Body:**
  ```json
  {
    "username": "your_username",
    "password": "your_password"
  }
  ```
- **Response:**
  ```json
  {
    "status": "ok",
    "username": "your_username",
    "token": "tok_abb4ed7dcad1a1f5..."
  }
  ```

---

### 2. Chat & Inference
- **Method:** `POST /api/chat`
- **Headers:** `Authorization: Bearer <token>`
- **Body:**
  ```json
  {
    "text": "I want to train for a half marathon and I have asthma"
  }
  ```
- **Response:**
  ```json
  {
    "reply": "Training for a half marathon with asthma requires...",
    "memories_saved": ["Goal: Running / endurance training"],
    "provider": "gemini-2.5-flash",
    "batched_with": 3
  }
  ```

---

### 3. Health & System Metrics
- **Method:** `GET /health`
- **Response:**
  ```json
  {
    "status": "ok",
    "runtime": "go",
    "ram_usage_mb": "1.2 MB",
    "uptime_seconds": 3600,
    "total_requests": 150,
    "upstream_calls_saved": 98
  }
  ```
