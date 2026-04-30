package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SimonWaldherr/mdexec/internal/aisafety"
	"github.com/SimonWaldherr/mdexec/internal/executor"
	"github.com/SimonWaldherr/mdexec/internal/policy"
	"github.com/SimonWaldherr/mdexec/internal/task"
)

func TestExecuteTaskOrderBlocksUnsafeAIDecision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"safe\":false,\"reason\":\"removes important files\"}"}}]}`))
	}))
	defer server.Close()

	tasks := []*task.Task{{
		Name: "unsafe",
		Lang: "bash",
		Code: "echo should not run",
	}}

	_, err := executeTaskOrder(context.Background(), tasks, executionConfig{
		Policy: policy.Default(),
		Run:    executor.Options{AllowLevel: policy.RiskLow},
		AI:     aisafety.Options{Enabled: true, Endpoint: server.URL + "/v1/chat/completions", Model: "local-model"},
		Yes:    true,
		Out:    &strings.Builder{},
		ErrOut: &strings.Builder{},
	})
	if err == nil {
		t.Fatal("executeTaskOrder returned nil error, want AI safety block")
	}
	if !strings.Contains(err.Error(), "blocked by AI safety check") {
		t.Fatalf("error = %q, want AI safety block", err)
	}
}
