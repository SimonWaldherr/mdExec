package aisafety

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseDecision(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    Decision
	}{
		{
			name:    "plain json",
			content: `{"safe":true,"reason":"read-only command"}`,
			want:    Decision{Safe: true, Reason: "read-only command"},
		},
		{
			name:    "json in surrounding text",
			content: "decision:\n{\"safe\":false,\"reason\":\"removes files\"}\n",
			want:    Decision{Safe: false, Reason: "removes files"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDecision(tc.content)
			if err != nil {
				t.Fatalf("ParseDecision returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ParseDecision() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestIsOllamaEndpoint(t *testing.T) {
	tests := []struct {
		provider string
		endpoint string
		want     bool
	}{
		{"auto", "http://localhost:11434/api/chat", true},
		{"auto", "http://localhost:11434/api/generate", true},
		{"auto", "http://localhost:1234/v1/chat/completions", false},
		{"auto", "http://localhost:1234/api/local/v1/chat/completions", false},
		{"auto", "http://host11434.example/v1/chat/completions", false},
		{"ollama", "http://localhost:1234/v1/chat/completions", true},
		{"openai", "http://localhost:11434/api/chat", false},
	}
	for _, tc := range tests {
		if got := shouldUseOllama(tc.provider, tc.endpoint); got != tc.want {
			t.Errorf("shouldUseOllama(%q, %q) = %v, want %v", tc.provider, tc.endpoint, got, tc.want)
		}
	}
}

func TestCheckOpenAICompatible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
			return
		}
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.Model != "local-model" {
			t.Errorf("model = %q, want local-model", req.Model)
			return
		}
		if len(req.Messages) != 1 || req.Messages[0].Content == "" {
			t.Errorf("missing prompt in request: %+v", req)
			return
		}
		for _, want := range []string{"Task: hello", "Language: bash", "echo hello"} {
			if !strings.Contains(req.Messages[0].Content, want) {
				t.Errorf("prompt %q does not contain %q", req.Messages[0].Content, want)
				return
			}
		}
		if _, err := w.Write([]byte(`{"choices":[{"message":{"content":"{\"safe\":true,\"reason\":\"echo only\"}"}}]}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	got, err := Check(context.Background(), Options{Enabled: true, Endpoint: server.URL + "/v1/chat/completions", Model: "local-model"}, Request{
		TaskName: "hello",
		Language: "bash",
		Code:     "echo hello",
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !got.Safe || got.Reason != "echo only" {
		t.Fatalf("Check() = %+v, want safe echo decision", got)
	}
}

func TestCheckOllama(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s, want /api/chat", r.URL.Path)
			return
		}
		if _, err := w.Write([]byte(`{"message":{"content":"{\"safe\":false,\"reason\":\"destructive delete\"}"}}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	got, err := Check(context.Background(), Options{Enabled: true, Endpoint: server.URL + "/api/chat", Model: "local-model"}, Request{
		TaskName: "clean",
		Language: "bash",
		Code:     "rm -rf /tmp/demo",
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if got.Safe || got.Reason != "destructive delete" {
		t.Fatalf("Check() = %+v, want unsafe destructive delete decision", got)
	}
}
