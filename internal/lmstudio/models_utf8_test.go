package lmstudio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/irinery/middlewareAuth/internal/security"
)

func TestListModelsRejectsInvalidUTF8WithoutInventingID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(append([]byte(`{"data":[{"id":"model-`), append([]byte{0xff}, []byte(`"}]}`)...)...))
	}))
	defer server.Close()
	models, err := NewTransport(server.Client()).ListModels(context.Background(), server.URL, "fixture-key")
	if err == nil || security.Code(err) != "ERR_LMSTUDIO_RESPONSE_INVALID" || len(models) != 0 {
		t.Fatalf("invalid UTF-8 was not rejected: models=%d code=%s", len(models), security.Code(err))
	}
}

func TestListModelsRejectsUnpairedUnicodeSurrogates(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
		wantID     string
	}{
		{"high-alone", `{"data":[{"id":"x\ud800"}]}`, false, ""},
		{"low-alone", `{"data":[{"id":"x\udc00"}]}`, false, ""},
		{"wrong-pair", `{"data":[{"id":"x\ud800\u0041"}]}`, false, ""},
		{"valid-pair", `{"data":[{"id":"x\ud83d\ude80"}]}`, true, "x🚀"},
		{"escaped-backslash", `{"data":[{"id":"x\\ud800"}]}`, true, `x\ud800`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer server.Close()
			models, err := NewTransport(server.Client()).ListModels(context.Background(), server.URL, "fixture-key")
			if tc.valid && (err != nil || len(models) != 1) {
				t.Fatalf("valid response rejected: %v", err)
			}
			if tc.valid && models[0].ID != tc.wantID {
				t.Fatalf("model ID changed: %q", models[0].ID)
			}
			if !tc.valid && (err == nil || security.Code(err) != "ERR_LMSTUDIO_RESPONSE_INVALID" || len(models) != 0) {
				t.Fatal("invalid surrogate accepted")
			}
		})
	}
}
