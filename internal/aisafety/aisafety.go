package aisafety

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultEndpoint = "http://localhost:11434/api/chat"

type Options struct {
	Enabled  bool
	Endpoint string
	Model    string
	Timeout  time.Duration
}

type Request struct {
	TaskName string
	Language string
	Code     string
}

type Decision struct {
	Safe   bool   `json:"safe"`
	Reason string `json:"reason"`
}

func Check(ctx context.Context, opts Options, req Request) (Decision, error) {
	if opts.Endpoint == "" {
		opts.Endpoint = defaultEndpoint
	}
	if opts.Model == "" {
		return Decision{}, fmt.Errorf("AI safety model is required")
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	prompt := safetyPrompt(req)
	if isOllamaEndpoint(opts.Endpoint) {
		return checkOllama(ctx, opts, prompt)
	}
	return checkOpenAICompatible(ctx, opts, prompt)
}

func safetyPrompt(req Request) string {
	return strings.TrimSpace(fmt.Sprintf(`You are reviewing a command extracted from a Markdown code block before it runs locally.
Decide whether it is safe to execute on a developer workstation.
Consider destructive file operations, privilege escalation, credential exposure, network downloads piped to shells, persistence, data exfiltration, and unexpected system changes.
Return only compact JSON with this exact shape: {"safe":true|false,"reason":"short reason"}.

Task: %s
Language: %s
Command:
%s`, req.TaskName, req.Language, req.Code))
}

func isOllamaEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	return u.Path == "/api/chat" || u.Path == "/api/generate" || u.Port() == "11434"
}

func checkOllama(ctx context.Context, opts Options, prompt string) (Decision, error) {
	payload := map[string]any{
		"model":  opts.Model,
		"stream": false,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"format": "json",
	}
	body, err := postJSON(ctx, opts.Endpoint, payload)
	if err != nil {
		return Decision{}, err
	}
	var resp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Decision{}, fmt.Errorf("decode AI safety response: %w", err)
	}
	if resp.Error != "" {
		return Decision{}, fmt.Errorf("AI safety service error: %s", resp.Error)
	}
	content := resp.Message.Content
	if content == "" {
		content = resp.Response
	}
	return ParseDecision(content)
}

func checkOpenAICompatible(ctx context.Context, opts Options, prompt string) (Decision, error) {
	payload := map[string]any{
		"model": opts.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0,
	}
	body, err := postJSON(ctx, opts.Endpoint, payload)
	if err != nil {
		return Decision{}, err
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Decision{}, fmt.Errorf("decode AI safety response: %w", err)
	}
	if resp.Error != nil {
		return Decision{}, fmt.Errorf("AI safety service error: %v", resp.Error)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return Decision{}, fmt.Errorf("AI safety response did not include a decision")
	}
	return ParseDecision(resp.Choices[0].Message.Content)
}

func postJSON(ctx context.Context, endpoint string, payload any) ([]byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call AI safety service: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("AI safety service returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func ParseDecision(content string) (Decision, error) {
	content = strings.TrimSpace(content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return Decision{}, fmt.Errorf("AI safety response was not JSON")
	}
	var d Decision
	if err := json.Unmarshal([]byte(content[start:end+1]), &d); err != nil {
		return Decision{}, fmt.Errorf("parse AI safety decision: %w", err)
	}
	if d.Reason == "" {
		if d.Safe {
			d.Reason = "model marked command safe"
		} else {
			d.Reason = "model marked command unsafe"
		}
	}
	return d, nil
}
