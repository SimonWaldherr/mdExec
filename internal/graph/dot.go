package graph

import (
	"fmt"
	"os"
	"strings"

	"github.com/SimonWaldherr/mdexec/internal/task"
)

func WriteDOT(tasks []*task.Task, out string) error {
	var b strings.Builder
	b.WriteString("digraph mdexec {\n  rankdir=\"LR\";\n")
	for _, t := range tasks {
		label := fmt.Sprintf("%s\\n[%s]\\n%s", t.Name, t.Lang, t.Path())
		fmt.Fprintf(&b, "  %q [label=%q, shape=box];\n", t.Name, label)
	}
	for _, t := range tasks {
		for _, d := range t.Deps {
			if contains(tasks, d) {
				fmt.Fprintf(&b, "  %q -> %q;\n", d, t.Name)
			}
		}
	}
	b.WriteString("}\n")
	return os.WriteFile(out, []byte(b.String()), 0o644)
}

func contains(ts []*task.Task, name string) bool {
	for _, t := range ts {
		if t.Name == name {
			return true
		}
	}
	return false
}
