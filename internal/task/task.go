package task

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type Task struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Lang      string            `json:"lang"`
	Code      string            `json:"code"`
	Heading   []string          `json:"heading"`
	Deps      []string          `json:"deps"`
	Tags      []string          `json:"tags"`
	Dir       string            `json:"dir,omitempty"`
	Shell     string            `json:"shell,omitempty"`
	Continue  bool              `json:"continue"`
	Timeout   int               `json:"timeout,omitempty"`
	StartLine int               `json:"start_line"`
	EndLine   int               `json:"end_line"`
	File      string            `json:"file"`
	Info      map[string]string `json:"info"`
	Marked    bool              `json:"marked"` // had mdexec tag

	// Risk & policy
	RiskScore   int      `json:"risk_score"`
	RiskReasons []string `json:"risk_reasons"`
}

func (t Task) Path() string {
	return strings.Join(t.Heading, "/")
}

func (t Task) Label() string {
	p := t.Path()
	if p == "" {
		p = "<root>"
	}
	return fmt.Sprintf("%s (%s) @ %s", t.Name, t.Lang, p)
}

type Index struct {
	Tasks  []Task
	ByName map[string]*Task
}

func NewIndex(tasks []Task) *Index {
	idx := &Index{Tasks: tasks, ByName: map[string]*Task{}}
	for i := range tasks {
		t := &tasks[i]
		idx.ByName[t.Name] = t
	}
	return idx
}

type Filter struct {
	Names []string
	Under string
	Lang  string
	Tags  []string
	Regex *regexp.Regexp
}

func (idx *Index) Filter(f Filter) []Task {
	res := idx.Tasks
	if len(f.Names) > 0 {
		set := map[string]struct{}{}
		for _, n := range f.Names {
			set[n] = struct{}{}
		}
		tmp := []Task{}
		for _, t := range res {
			if _, ok := set[t.Name]; ok {
				tmp = append(tmp, t)
			}
		}
		res = tmp
	}
	if f.Under != "" {
		parts := []string{}
		for _, p := range strings.Split(strings.Trim(f.Under, "# "), "/") {
			if q := strings.TrimSpace(p); q != "" {
				parts = append(parts, q)
			}
		}
		if len(parts) > 0 {
			match := func(h []string, parts []string) bool {
				if len(h) >= len(parts) && equal(h[:len(parts)], parts) {
					return true
				}
				if len(h) >= len(parts) && equal(h[len(h)-len(parts):], parts) {
					return true
				}
				for i := 0; i+len(parts) <= len(h); i++ {
					if equal(h[i:i+len(parts)], parts) {
						return true
					}
				}
				return false
			}
			tmp := []Task{}
			for _, t := range res {
				if match(t.Heading, parts) {
					tmp = append(tmp, t)
				}
			}
			res = tmp
		}
	}
	if f.Lang != "" {
		tmp := []Task{}
		for _, t := range res {
			if t.Lang == f.Lang {
				tmp = append(tmp, t)
			}
		}
		res = tmp
	}
	if len(f.Tags) > 0 {
		tset := map[string]struct{}{}
		for _, tg := range f.Tags {
			tset[tg] = struct{}{}
		}
		tmp := []Task{}
		for _, t := range res {
			hit := false
			for _, tg := range t.Tags {
				if _, ok := tset[tg]; ok {
					hit = true
					break
				}
			}
			if hit {
				tmp = append(tmp, t)
			}
		}
		res = tmp
	}
	if f.Regex != nil {
		tmp := []Task{}
		for _, t := range res {
			if f.Regex.MatchString(t.Name) || f.Regex.MatchString(t.Path()) {
				tmp = append(tmp, t)
			}
		}
		res = tmp
	}
	return res
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Topo sort preserving dependencies among the selected set (closure)
func (idx *Index) TopoSort(selected []Task) ([]*Task, error) {
	want := map[string]*Task{}
	stack := []*Task{}
	for i := range selected {
		t := idx.ByName[selected[i].Name]
		want[t.Name] = t
		stack = append(stack, t)
	}
	for len(stack) > 0 {
		last := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, d := range last.Deps {
			dep, ok := idx.ByName[d]
			if !ok {
				return nil, fmt.Errorf("task '%s' depends on unknown '%s'", last.Name, d)
			}
			if _, seen := want[dep.Name]; !seen {
				want[dep.Name] = dep
				stack = append(stack, dep)
			}
		}
	}
	indeg := map[string]int{}
	adj := map[string][]string{}
	for name := range want {
		indeg[name] = 0
		adj[name] = nil
	}
	for name, t := range want {
		for _, d := range t.Deps {
			if _, ok := want[d]; ok {
				indeg[name]++
				adj[d] = append(adj[d], name)
			}
		}
	}
	q := []string{}
	for n, deg := range indeg {
		if deg == 0 {
			q = append(q, n)
		}
	}
	order := []*Task{}
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		order = append(order, want[n])
		for _, m := range adj[n] {
			indeg[m]--
			if indeg[m] == 0 {
				q = append(q, m)
			}
		}
	}
	if len(order) != len(want) {
		cycle := []string{}
		for n, d := range indeg {
			if d > 0 {
				cycle = append(cycle, n)
			}
		}
		return nil, fmt.Errorf("dependency cycle among: %s", strings.Join(cycle, ", "))
	}
	return order, nil
}

// Pretty table render helper
func FormatTable(tasks []Task) string {
	if len(tasks) == 0 {
		return "No runnable tasks found with given filters."
	}
	nameW, langW, pathW, depsW := len("NAME"), len("LANG"), len("PATH"), len("DEPS")
	rows := make([][]string, 0, len(tasks))
	for _, t := range tasks {
		deps := strings.Join(t.Deps, ", ")
		row := []string{t.Name, t.Lang, t.Path(), deps, strings.Join(t.Tags, ", ")}
		rows = append(rows, row)
		if len(t.Name) > nameW {
			nameW = len(t.Name)
		}
		if len(t.Lang) > langW {
			langW = len(t.Lang)
		}
		if len(t.Path()) > pathW {
			pathW = len(t.Path())
		}
		if len(deps) > depsW {
			depsW = len(deps)
		}
	}
	var sb strings.Builder
	header := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %s\n",
		nameW, "NAME", langW, "LANG", pathW, "PATH", depsW, "DEPS", "TAGS")
	sb.WriteString(header)
	for _, r := range rows {
		line := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %s\n",
			nameW, r[0], langW, r[1], pathW, r[2], depsW, r[3], r[4])
		sb.WriteString(line)
	}
	return sb.String()
}

// Sorting helper for stable output
func SortByPathThenName(ts []Task) {
	sort.Slice(ts, func(i, j int) bool {
		pi := strings.Join(ts[i].Heading, "/")
		pj := strings.Join(ts[j].Heading, "/")
		if pi == pj {
			return ts[i].Name < ts[j].Name
		}
		return pi < pj
	})
}
