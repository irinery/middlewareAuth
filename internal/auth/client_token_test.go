package auth

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentClientTokenRotationsAllowOneWinner(t *testing.T) {
	const bootstrap = "bootstrap-token-with-more-than-32-characters"
	store, err := NewClientTokenStore(t.TempDir(), bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := store.Rotate(bootstrap)
			if err == nil {
				results <- token
			}
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for token := range results {
		count++
		if !store.Valid(token) {
			t.Fatal("winning token was not activated")
		}
	}
	if count != 1 {
		t.Fatalf("successful rotations=%d, want 1", count)
	}
}

func TestClientTokenStoreRejectsCorruptPersistedState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, clientTokenFile), []byte(`{"version":1,"sha256":"bad"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClientTokenStore(dir, "bootstrap-token-with-more-than-32-characters"); err == nil {
		t.Fatal("corrupt token store accepted bootstrap credential")
	}
}
