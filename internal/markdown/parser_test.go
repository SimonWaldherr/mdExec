package markdown

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello-world"},
		{"  Hello  World  ", "hello-world"},
		{"foo/bar", "foo-bar"},
		{"", "unnamed"},
		{"123", "123"},
		{"Hello-World", "hello-world"},
		{"---", "unnamed"},
	}
	for _, tc := range tests {
		got := slugify(tc.input)
		if got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"  ", nil},
		{"a,b,c", []string{"a", "b", "c"}},
		{"a, b , c", []string{"a", "b", "c"}},
		{"single", []string{"single"}},
		{",,,", nil},
	}
	for _, tc := range tests {
		got := splitCSV(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

func writeTempMD(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "test.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	return p
}

func TestParseMarkdown_WithMarker(t *testing.T) {
	md := writeTempMD(t, "# Setup\n\n```bash mdexec name=install\necho hello\n```\n")
	tasks, err := ParseMarkdown(md, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Name != "install" {
		t.Errorf("expected name 'install', got %q", tasks[0].Name)
	}
	if tasks[0].Lang != "bash" {
		t.Errorf("expected lang 'bash', got %q", tasks[0].Lang)
	}
	if tasks[0].Code != "echo hello" {
		t.Errorf("expected code 'echo hello', got %q", tasks[0].Code)
	}
}

func TestParseMarkdown_WithoutMarker_IncludeAll(t *testing.T) {
	md := writeTempMD(t, "# Build\n\n```bash\nmake build\n```\n")
	tasks, err := ParseMarkdown(md, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task (includeAll), got %d", len(tasks))
	}
}

func TestParseMarkdown_WithoutMarker_NoIncludeAll(t *testing.T) {
	md := writeTempMD(t, "# Build\n\n```bash\nmake build\n```\n")
	tasks, err := ParseMarkdown(md, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks (no marker, no includeAll), got %d", len(tasks))
	}
}

func TestParseMarkdown_HeadingPath(t *testing.T) {
	md := writeTempMD(t, "# Section A\n## Subsection\n\n```bash mdexec name=sub\necho sub\n```\n")
	tasks, err := ParseMarkdown(md, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	path := tasks[0].Path()
	if path != "Section A/Subsection" {
		t.Errorf("expected path 'Section A/Subsection', got %q", path)
	}
}

func TestParseMarkdown_UniqueNames(t *testing.T) {
	md := writeTempMD(t, "```bash mdexec name=task\necho 1\n```\n```bash mdexec name=task\necho 2\n```\n")
	tasks, err := ParseMarkdown(md, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	if tasks[0].Name == tasks[1].Name {
		t.Errorf("expected unique names, both are %q", tasks[0].Name)
	}
}

func TestParseMarkdown_Timeout(t *testing.T) {
	md := writeTempMD(t, "```bash mdexec name=timed timeout=30\necho timed\n```\n")
	tasks, err := ParseMarkdown(md, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Timeout != 30 {
		t.Errorf("expected timeout=30, got %d", tasks[0].Timeout)
	}
}
