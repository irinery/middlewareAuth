package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/irinery/middlewareAuth/internal/config"
)

func TestInheritedListenerValidation(t *testing.T) {
	if _, err := inheritedListener("", config.HTTPConfig{}); err == nil {
		t.Fatal("accepted empty descriptor")
	}
	if _, err := inheritedListener("2", config.HTTPConfig{}); err == nil {
		t.Fatal("accepted standard descriptor")
	}
	if _, err := inheritedListener("not-a-fd", config.HTTPConfig{}); err == nil {
		t.Fatal("accepted non-numeric descriptor")
	}
	for _, tc := range []struct {
		name, bind string
		wrongPort  bool
	}{
		{"port", "127.0.0.1", true}, {"address", "127.0.0.1", false}, {"wildcard", "0.0.0.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := net.Listen("tcp4", net.JoinHostPort(tc.bind, "0"))
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			file, err := raw.(*net.TCPListener).File()
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			fd, err := syscall.Dup(int(file.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			port := raw.Addr().(*net.TCPAddr).Port
			if tc.wrongPort {
				port++
			}
			configuredAddr := "127.0.0.1"
			if tc.name == "address" {
				configuredAddr = "127.0.0.2"
			}
			listener, err := inheritedListener(strconv.Itoa(fd), config.HTTPConfig{BindAddr: configuredAddr, Port: port})
			if err == nil {
				listener.Close()
				t.Fatal("accepted mismatched listener")
			}
		})
	}
}

func TestMiddlewareChild(t *testing.T) {
	if os.Getenv("NH_TEST_CHILD") != "1" {
		return
	}
	main()
}

func TestMiddlewareProcessCatalog(t *testing.T) {
	const clientToken = "fixture-client-token-32-characters-long"
	const upstreamKey = "fixture-upstream-key-nh-e2"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer "+upstreamKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"data":[{"id":"model-z"},{"id":"model-a"}]}`)
	}))
	defer upstream.Close()
	raw, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	file, err := raw.(*net.TCPListener).File()
	if err != nil {
		raw.Close()
		t.Fatal(err)
	}
	port := strconv.Itoa(raw.Addr().(*net.TCPAddr).Port)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestMiddlewareChild$")
	cmd.Dir = t.TempDir()
	cmd.ExtraFiles = []*os.File{file}
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.TempDir(), "NH_TEST_CHILD=1", "MIDDLEWARE_INHERITED_LISTENER_FD=3", "HTTP_BIND_ADDR=127.0.0.1", "HTTP_PORT=" + port, "OAUTH_CALLBACK_PORT=" + port, "MIDDLEWARE_STATE_DIR=" + t.TempDir(), "MIDDLEWARE_SECRET_KEY=fixture-encryption-key-32-characters-long", "MIDDLEWARE_CLIENT_TOKEN=" + clientToken, "MIDDLEWARE_REDACT_LOGS=true"}
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		file.Close()
		raw.Close()
		t.Fatal(err)
	}
	file.Close()
	raw.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if strings.Contains(logs.String(), clientToken) || strings.Contains(logs.String(), upstreamKey) {
			t.Error("credential leaked in process log")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	base := "http://127.0.0.1:" + port
	unauthorized, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/projects/pockettrace/llm/providers", nil)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Header.Set("Authorization", "Bearer incorrect-fixture-token")
	denied, err := http.DefaultClient.Do(unauthorized)
	if err != nil {
		t.Fatalf("process request: %v", err)
	}
	denied.Body.Close()
	if denied.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", denied.StatusCode)
	}
	request := func(method, path string, body []byte) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+clientToken)
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("process request: %v", err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(upstreamKey)) || bytes.Contains(data, []byte(clientToken)) {
			t.Fatal("credential leaked in response")
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("process status=%d", response.StatusCode)
		}
		return data
	}
	body, err := json.Marshal(map[string]any{"providerId": "lmstudio", "profileId": "work", "mode": "api_key", "authFields": map[string]any{"baseUrl": upstream.URL, "apiKey": upstreamKey}})
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, "/v1/projects/pockettrace/llm/login", body)
	for _, tc := range []struct {
		profile string
		want    bool
	}{{"work", true}, {"other", false}} {
		data := request(http.MethodGet, "/v1/projects/pockettrace/llm/providers?profileId="+tc.profile, nil)
		var catalog struct {
			Providers []struct {
				ID     string `json:"id"`
				Models []struct {
					ID string `json:"id"`
				} `json:"models"`
			} `json:"providers"`
		}
		if err := json.Unmarshal(data, &catalog); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, provider := range catalog.Providers {
			if provider.ID == "lmstudio" {
				found = true
				got := fmt.Sprint(provider.Models)
				if tc.want && got != "[{model-a} {model-z}]" {
					t.Fatalf("models %s", got)
				}
				if !tc.want && len(provider.Models) != 0 {
					t.Fatalf("profile leak %s", got)
				}
			}
		}
		if !found {
			t.Fatal("provider missing")
		}
	}
}
