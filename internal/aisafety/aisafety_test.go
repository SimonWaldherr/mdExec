package aisafety

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestCheckOpenAICompatible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "local-model" {
			t.Fatalf("model = %q, want local-model", req.Model)
		}
		if len(req.Messages) != 1 || req.Messages[0].Content == "" {
			t.Fatalf("missing prompt in request: %+v", req)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"safe\":true,\"reason\":\"echo only\"}"}}]}`))
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
			t.Fatalf("path = %s, want /api/chat", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"message":{"content":"{\"safe\":false,\"reason\":\"destructive delete\"}"}}`))
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
