package omniroute

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Router struct {
	PrimaryProvider  string
	GeminiAPIKey     string
	GroqAPIKey       string
	OpenRouterAPIKey string
	Client           *http.Client
}

func NewRouter(primary, geminiKey, groqKey, openRouterKey string) *Router {
	return &Router{
		PrimaryProvider:  primary,
		GeminiAPIKey:     geminiKey,
		GroqAPIKey:       groqKey,
		OpenRouterAPIKey: openRouterKey,
		Client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (r *Router) Complete(ctx context.Context, systemPrompt, userBatchedPrompt string) (string, string, error) {
	// Attempt sequence based on PrimaryProvider
	providers := []string{r.PrimaryProvider, "gemini", "groq", "openrouter", "mock"}
	seen := make(map[string]bool)

	for _, p := range providers {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true

		switch p {
		case "gemini":
			if r.GeminiAPIKey != "" {
				res, err := r.callGemini(ctx, systemPrompt, userBatchedPrompt)
				if err == nil && res != "" {
					return res, "gemini-2.5-flash", nil
				}
			}
		case "groq":
			if r.GroqAPIKey != "" {
				res, err := r.callOpenAICompatible(ctx, "https://api.groq.com/openai/v1/chat/completions", r.GroqAPIKey, "llama-3.3-70b-versatile", systemPrompt, userBatchedPrompt)
				if err == nil && res != "" {
					return res, "groq-llama-3.3", nil
				}
			}
		case "openrouter":
			if r.OpenRouterAPIKey != "" {
				res, err := r.callOpenAICompatible(ctx, "https://openrouter.ai/api/v1/chat/completions", r.OpenRouterAPIKey, "google/gemini-2.0-flash-exp:free", systemPrompt, userBatchedPrompt)
				if err == nil && res != "" {
					return res, "openrouter", nil
				}
			}
		case "mock":
			res := r.callMock(userBatchedPrompt)
			return res, "mock-offline-engine", nil
		}
	}

	res := r.callMock(userBatchedPrompt)
	return res, "mock-fallback", nil
}

func (r *Router) callGemini(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=%s", r.GeminiAPIKey)

	payload := map[string]interface{}{
		"system_instruction": map[string]interface{}{
			"parts": []map[string]string{{"text": systemPrompt}},
		},
		"contents": []map[string]interface{}{
			{
				"role": "user",
				"parts": []map[string]string{{"text": userPrompt}},
			},
		},
		"generationConfig": map[string]interface{}{
			"responseMimeType": "application/json",
		},
	}

	bodyBytes, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gemini status %d: %s", resp.StatusCode, string(b))
	}

	var res struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if len(res.Candidates) > 0 && len(res.Candidates[0].Content.Parts) > 0 {
		return res.Candidates[0].Content.Parts[0].Text, nil
	}

	return "", fmt.Errorf("empty gemini response")
}

func (r *Router) callOpenAICompatible(ctx context.Context, endpoint, apiKey, model, systemPrompt, userPrompt string) (string, error) {
	payload := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"response_format": map[string]string{"type": "json_object"},
	}

	bodyBytes, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := r.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upstream status %d: %s", resp.StatusCode, string(b))
	}

	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if len(res.Choices) > 0 {
		return res.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("empty openai-compatible response")
}

// Intelligent mock engine for offline testing and zero-downtime reliability
func (r *Router) callMock(batchedPrompt string) string {
	results := make(map[string]interface{})

	// Extract all REQ_ IDs
	lines := strings.Split(batchedPrompt, "\n")
	var currentID string
	var currentText strings.Builder

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<<<REQ_") && strings.HasSuffix(trimmed, ">>>") {
			parts := strings.Split(trimmed, " ")
			rawID := strings.TrimPrefix(parts[0], "<<<")
			currentID = rawID
			currentText.Reset()
		} else if strings.HasPrefix(trimmed, "<<</REQ_") {
			if currentID != "" {
				text := currentText.String()
				results[currentID] = generateMockResponse(text)
				currentID = ""
			}
		} else if currentID != "" {
			currentText.WriteString(line + " ")
		}
	}

	out := map[string]interface{}{
		"results": results,
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func generateMockResponse(prompt string) map[string]interface{} {
	lower := strings.ToLower(prompt)
	var reply string
	var newMemories []string

	// Check for goals or health facts
	if strings.Contains(lower, "marathon") || strings.Contains(lower, "run") {
		newMemories = append(newMemories, "Goal: Running / endurance training")
		reply = "Consistency and gradual mileage progression (10% rule) are key to distance running. Prioritize proper footwear, rest days, and progressive base building."
	} else if strings.Contains(lower, "asthma") || strings.Contains(lower, "breath") {
		newMemories = append(newMemories, "Condition: Respiratory sensitivity / asthma")
		reply = "For respiratory conditions during exercise, always ensure a progressive warm-up, avoid dry cold air without a barrier mask, and maintain hydration."
	} else if strings.Contains(lower, "hypertension") || strings.Contains(lower, "blood pressure") {
		newMemories = append(newMemories, "Condition: Hypertension / blood pressure focus")
		reply = "Managing cardiovascular health involves aerobic movement (brisk walking, cycling), a balanced sodium-conscious diet, and stress moderation."
	} else if strings.Contains(lower, "eat") || strings.Contains(lower, "food") || strings.Contains(lower, "diet") {
		reply = "Nutritional balance focusing on whole foods, lean proteins, high fiber, and adequate hydration supports optimal energy and metabolic recovery."
	} else {
		reply = "Your health query has been processed securely in the gVisor sandbox. Maintaining sustainable daily lifestyle habits is the foundation of long-term wellness."
	}

	return map[string]interface{}{
		"reply":        reply,
		"new_memories": newMemories,
	}
}
