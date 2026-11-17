package tests

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/sagar-anmol/backendofthehealthapp/internal/auth"
	"github.com/sagar-anmol/backendofthehealthapp/internal/clubber"
	"github.com/sagar-anmol/backendofthehealthapp/internal/db"
	"github.com/sagar-anmol/backendofthehealthapp/internal/omniroute"
)

func TestAuthAndDatabase(t *testing.T) {
	testDBPath := "test_auth.db"
	defer os.Remove(testDBPath)

	database, err := db.InitDB(testDBPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer database.Close()

	hash, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	token := auth.GenerateToken()
	user, err := database.CreateUser("alice", hash, token)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	if user.Username != "alice" || user.Token != token {
		t.Errorf("Unexpected user values: %+v", user)
	}

	// Verify CheckPassword
	if !auth.CheckPassword(user.PasswordHash, "secret123") {
		t.Errorf("CheckPassword failed on correct password")
	}
	if auth.CheckPassword(user.PasswordHash, "wrongpass") {
		t.Errorf("CheckPassword passed on wrong password")
	}

	// Test Memory Persistence
	err = database.SaveMemory(user.ID, "Goal: Run a marathon")
	if err != nil {
		t.Fatalf("SaveMemory failed: %v", err)
	}

	memories, err := database.GetMemories(user.ID)
	if err != nil || len(memories) != 1 || memories[0] != "Goal: Run a marathon" {
		t.Errorf("Unexpected memories returned: %v", memories)
	}
}

func TestConcurrentClubbingIsolation(t *testing.T) {
	testDBPath := "test_clubber.db"
	defer os.Remove(testDBPath)

	database, err := db.InitDB(testDBPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer database.Close()

	router := omniroute.NewRouter("mock", "", "", "")
	club := clubber.NewClubber(database, router, 5, 100*time.Millisecond)

	// Launch 3 concurrent requests simultaneously from different users
	queries := []struct {
		userID   int64
		username string
		query    string
		expected string
	}{
		{userID: 1, username: "user_marathon", query: "I want to train for a marathon", expected: "marathon"},
		{userID: 2, username: "user_asthma", query: "I have mild asthma during winter", expected: "asthma"},
		{userID: 3, username: "user_diet", query: "What should I eat for recovery?", expected: "diet"},
	}

	var wg sync.WaitGroup
	results := make([]clubber.Response, len(queries))

	start := time.Now()
	for i, q := range queries {
		wg.Add(1)
		go func(idx int, uID int64, uName, txt string) {
			defer wg.Done()
			ch := club.Submit(clubber.UserRequest{
				UserID:   uID,
				Username: uName,
				Text:     txt,
			})
			results[idx] = <-ch
		}(i, q.userID, q.username, q.query)
	}

	wg.Wait()
	duration := time.Since(start)

	t.Logf("Clubbing finished in %v", duration)

	for i, res := range results {
		if res.Error != nil {
			t.Errorf("Query %d failed with error: %v", i, res.Error)
		}
		if res.Reply == "" {
			t.Errorf("Query %d returned empty reply", i)
		}
		// Confirm all were clubbed into the same batch!
		if res.BatchedWith < 2 {
			t.Errorf("Expected request %d to be batched with at least 2 requests, got: %d", i, res.BatchedWith)
		}
		t.Logf("User %d got reply (batched with %d): %s", i+1, res.BatchedWith, res.Reply[:40]+"...")
	}

	totalReqs, batched, upstream := club.GetStats()
	t.Logf("Stats: totalReqs=%d, batched=%d, upstreamCalls=%d (Calls Saved=%d)",
		totalReqs, batched, upstream, batched-upstream)

	if upstream >= batched {
		t.Errorf("Expected upstream calls (%d) to be strictly less than batched requests (%d)", upstream, batched)
	}
}
