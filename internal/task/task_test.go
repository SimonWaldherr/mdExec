package task

import (
	"testing"
)

func TestFilter_ByName(t *testing.T) {
	tasks := []Task{
		{ID: "t1", Name: "build", Lang: "bash"},
		{ID: "t2", Name: "test", Lang: "bash"},
		{ID: "t3", Name: "deploy", Lang: "bash"},
	}
	idx := NewIndex(tasks)
	got := idx.Filter(Filter{Names: []string{"build", "deploy"}})
	if len(got) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(got))
	}
}

func TestFilter_ByLang(t *testing.T) {
	tasks := []Task{
		{ID: "t1", Name: "py-task", Lang: "python"},
		{ID: "t2", Name: "sh-task", Lang: "bash"},
	}
	idx := NewIndex(tasks)
	got := idx.Filter(Filter{Lang: "python"})
	if len(got) != 1 || got[0].Name != "py-task" {
		t.Fatalf("expected 1 python task, got %v", got)
	}
}

func TestFilter_ByTags(t *testing.T) {
	tasks := []Task{
		{ID: "t1", Name: "a", Tags: []string{"ci", "build"}},
		{ID: "t2", Name: "b", Tags: []string{"release"}},
		{ID: "t3", Name: "c", Tags: []string{"ci"}},
	}
	idx := NewIndex(tasks)
	got := idx.Filter(Filter{Tags: []string{"ci"}})
	if len(got) != 2 {
		t.Fatalf("expected 2 tasks with tag 'ci', got %d", len(got))
	}
}

func TestFilter_ByUnder(t *testing.T) {
	tasks := []Task{
		{ID: "t1", Name: "a", Heading: []string{"Setup", "Install"}},
		{ID: "t2", Name: "b", Heading: []string{"Setup", "Configure"}},
		{ID: "t3", Name: "c", Heading: []string{"Teardown"}},
	}
	idx := NewIndex(tasks)
	got := idx.Filter(Filter{Under: "Setup"})
	if len(got) != 2 {
		t.Fatalf("expected 2 tasks under 'Setup', got %d", len(got))
	}
}

func TestTopoSort_NoDeps(t *testing.T) {
	tasks := []Task{
		{ID: "t1", Name: "a", Deps: nil},
		{ID: "t2", Name: "b", Deps: nil},
	}
	idx := NewIndex(tasks)
	order, err := idx.TopoSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 2 {
		t.Fatalf("expected 2 tasks in order, got %d", len(order))
	}
}

func TestTopoSort_WithDeps(t *testing.T) {
	tasks := []Task{
		{ID: "t1", Name: "a", Deps: nil},
		{ID: "t2", Name: "b", Deps: []string{"a"}},
		{ID: "t3", Name: "c", Deps: []string{"b"}},
	}
	idx := NewIndex(tasks)
	order, err := idx.TopoSort(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(order))
	}
	// "a" must come before "b", "b" must come before "c"
	pos := map[string]int{}
	for i, tt := range order {
		pos[tt.Name] = i
	}
	if pos["a"] >= pos["b"] {
		t.Errorf("expected 'a' before 'b', order: %v", pos)
	}
	if pos["b"] >= pos["c"] {
		t.Errorf("expected 'b' before 'c', order: %v", pos)
	}
}

func TestTopoSort_Cycle(t *testing.T) {
	tasks := []Task{
		{ID: "t1", Name: "a", Deps: []string{"b"}},
		{ID: "t2", Name: "b", Deps: []string{"a"}},
	}
	idx := NewIndex(tasks)
	_, err := idx.TopoSort(tasks)
	if err == nil {
		t.Fatal("expected error for cyclic dependency, got nil")
	}
}

func TestTopoSort_UnknownDep(t *testing.T) {
	tasks := []Task{
		{ID: "t1", Name: "a", Deps: []string{"nonexistent"}},
	}
	idx := NewIndex(tasks)
	_, err := idx.TopoSort(tasks)
	if err == nil {
		t.Fatal("expected error for unknown dependency, got nil")
	}
}

func TestFormatTable_Empty(t *testing.T) {
	out := FormatTable(nil)
	if out == "" {
		t.Fatal("expected non-empty output for empty task list")
	}
}

func TestFormatTable_Tasks(t *testing.T) {
	tasks := []Task{
		{Name: "mytask", Lang: "bash", Heading: []string{"Setup"}, Deps: nil, Tags: []string{"ci"}},
	}
	out := FormatTable(tasks)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
}
