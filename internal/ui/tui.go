package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SimonWaldherr/mdexec/internal/task"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type TUIOptions struct {
	Run    func(names []string) (string, error)
	Filter func(under string, tags []string) []task.Task
	TailLines int
}

func RunTUI(listTasks func() []task.Task, opts TUIOptions) error {
	app := tview.NewApplication()
	tree := tview.NewTreeView()
	tree.SetBorder(true).SetTitle("Tasks")
	info := tview.NewTextView().SetDynamicColors(true)
	preview := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	preview.SetBorder(true).SetTitle("Preview")
	help := tview.NewTextView().SetDynamicColors(true)
	help.SetText("[yellow]e:expand all  c:collapse all  Tab:toggle focus  Space:select  Enter:expand/collapse  r:run  o:open output  q:quit")
	help.SetBorder(true).SetTitle("Help")
	output := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	output.SetBorder(true).SetTitle("Output")
	// prepare a log file in temp dir for additional debugging
	var logFile *os.File
	if f, err := os.OpenFile(filepath.Join(os.TempDir(), "mdexec-ui.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		logFile = f
	}

	under := ""
	tags := ""
	selected := map[string]bool{}
	// channel used to signal completion of pipe-reading goroutines when capturing run output
	var done chan struct{}

	// live-tail settings: keep last N lines in memory and write full output to a temp file

	type runOutputState struct {
		mu      sync.Mutex
		lines   []string // last N lines (tail)
		leftover string  // incomplete line fragment
		file    *os.File // temp file storing full output
		tmpPath string
		total   int
	}

	var currentRun *runOutputState
	var currentRunMu sync.Mutex
	// source markdown file for preview (inferred from first listed task)
	var sourceFile string

	// Build a tree grouped by header path (split by '/') and attach tasks as leaves.
	buildTree := func() {
		root := tview.NewTreeNode("mdexec")
		nodes := map[string]*tview.TreeNode{"": root}
		selection := listTasks()
		if len(selection) > 0 {
			sourceFile = selection[0].File
		}
		if opts.Filter != nil {
			selection = opts.Filter(under, split(tags))
		}
		// preserve the original order returned by the parser (markdown heading order)
		for i := range selection {
			t := selection[i]
			// create path nodes
			parts := strings.Split(t.Path(), "/")
			curPath := ""
			parent := root
			for _, p := range parts {
				if p == "" {
					continue
				}
				if curPath == "" {
					curPath = p
				} else {
					curPath = curPath + "/" + p
				}
				if n, ok := nodes[curPath]; ok {
					parent = n
					continue
				}
				n := tview.NewTreeNode(p).SetSelectable(true)
				n.SetColor(tview.Styles.PrimaryTextColor)
				// attach a reference for heading nodes so we can show preview text
				// reference format: "H:" + joined path (e.g., "H:mdexec/TL;DR")
				n.SetReference("H:" + curPath)
				parent.AddChild(n)
				nodes[curPath] = n
				parent = n
			}
			// leaf node for task — create a stable copy to reference
			tt := t
			leafText := fmt.Sprintf("%s %s [%s]", func() string {
				if selected[tt.Name] {
					return "●"
				} else {
					return "○"
				}
			}(), tt.Name, tt.Lang)
			leaf := tview.NewTreeNode(leafText).SetReference(&tt).SetSelectable(true)
			// color by risk
			if tt.RiskScore >= 6 {
				leaf.SetColor(tcell.ColorRed)
			} else if tt.RiskScore >= 3 {
				leaf.SetColor(tcell.ColorYellow)
			} else {
				leaf.SetColor(tcell.ColorGreen)
			}
			parent.AddChild(leaf)
		}
		tree.SetRoot(root).SetCurrentNode(root)
		info.SetText("[yellow]Keys[-]: Space select • Enter preview • u set under • t set tags • r run • q quit")
	}

	// logging helper writes to output box and optional log file
	logf := func(format string, a ...interface{}) {
		line := fmt.Sprintf(format, a...)
		ts := time.Now().Format("2006-01-02 15:04:05")
		out := fmt.Sprintf("[%s] %s\n", ts, line)
		app.QueueUpdateDraw(func() { output.Write([]byte(out)) })
		if logFile != nil {
			_, _ = logFile.WriteString(out)
		}
	}

	// helpers to expand/collapse recursively
	var expandAll func(n *tview.TreeNode)
	var collapseAll func(n *tview.TreeNode)
	expandAll = func(n *tview.TreeNode) {
		if n == nil {
			return
		}
		n.SetExpanded(true)
		for _, c := range n.GetChildren() {
			expandAll(c)
		}
	}
	collapseAll = func(n *tview.TreeNode) {
		if n == nil {
			return
		}
		n.SetExpanded(false)
		for _, c := range n.GetChildren() {
			collapseAll(c)
		}
	}

	// no auto-timeout: allow long interactive sessions

	buildTree()

	// ensure layout is non-nil early so modal DoneFunc can safely reference it
	var layout tview.Primitive = tview.NewFlex()

	// When Enter is pressed on a node, show the code in a modal (if it's a task leaf)
	// Disable Enter/key-select opening a modal to avoid hangs; preview is available in the right pane.
	tree.SetSelectedFunc(func(node *tview.TreeNode) {
		// intentionally no-op: keep selection but do not open blocking modal
		_ = node
	})

	tree.SetChangedFunc(func(node *tview.TreeNode) {
		if node == nil {
			return
		}
		ref := node.GetReference()
		if ref == nil {
			preview.SetText("")
			return
		}
		// task leaf
		if tp, ok := ref.(*task.Task); ok {
			preview.SetText(fmt.Sprintf("%s\n\n%s", tp.Label(), tp.Code))
			return
		}
		// heading node
		if s, ok := ref.(string); ok && strings.HasPrefix(s, "H:") {
			pathStr := strings.TrimPrefix(s, "H:")
			text := extractHeadingText(sourceFile, strings.Split(pathStr, "/"))
			if strings.TrimSpace(text) == "" {
				preview.SetText(fmt.Sprintf("%s", pathStr))
			} else {
				preview.SetText(text)
			}
			return
		}
	})

	focusedOnTree := true
	// capture run output by temporarily redirecting stdout/stderr
	captureRun := func(names []string) {
		output.Clear()
		header := fmt.Sprintf("--- RUN: %s ---\n", strings.Join(names, ","))
		app.QueueUpdateDraw(func() { output.Write([]byte(header)) })
		if logFile != nil {
			_, _ = logFile.WriteString(header)
		}
		logf("Run start: %v", names)

		// Create pipes for capturing fd-level stdout/stderr and dup them onto fd 1/2
		rOut, wOut, _ := os.Pipe()
		rErr, wErr, _ := os.Pipe()

		// save original fds
		outFd := int(os.Stdout.Fd())
		errFd := int(os.Stderr.Fd())
		savedOut, _ := syscall.Dup(outFd)
		savedErr, _ := syscall.Dup(errFd)

		// dup pipe writers onto stdout/stderr fds
		_ = syscall.Dup2(int(wOut.Fd()), outFd)
		_ = syscall.Dup2(int(wErr.Fd()), errFd)

		// prepare run state and temp file for full output
		currentRunMu.Lock()
		// cleanup previous tmp if exists
		if currentRun != nil && currentRun.tmpPath != "" {
			_ = os.Remove(currentRun.tmpPath)
			if currentRun.file != nil {
				_ = currentRun.file.Close()
			}
		}
		f, _ := os.CreateTemp("", "mdexec-out-*.log")
		tailLinesLocal := opts.TailLines
		if tailLinesLocal <= 0 {
			tailLinesLocal = 5
		}
		rs := &runOutputState{file: f, tmpPath: f.Name(), lines: make([]string, 0, tailLinesLocal)}
		currentRun = rs
		currentRunMu.Unlock()

		// helper to display the current tail in the output box
		showTail := func() {
			rs.mu.Lock()
			defer rs.mu.Unlock()
			out := strings.Join(rs.lines, "\n")
			if out != "" {
				out += "\n"
			}
			app.QueueUpdateDraw(func() {
				output.Clear()
				output.Write([]byte(out))
			})
		}

		// read from pipes and write to temp file while keeping a tail in memory
		done = make(chan struct{})
		streamReader := func(r io.Reader) {
			defer func() {
				// signal if both readers done (closing done once is OK; we close only in the first goroutine to finish)
			}()
			buf := make([]byte, 1024)
			for {
				n, err := r.Read(buf)
				if n > 0 {
					b := buf[:n]
					// write full output to temp file
					if rs.file != nil {
						_, _ = rs.file.Write(b)
					}
					if logFile != nil {
						_, _ = logFile.Write(b)
					}
					// process lines for tail
					rs.mu.Lock()
					s := rs.leftover + string(b)
					parts := strings.Split(s, "\n")
					// if last char is not newline, last part is leftover
					if !strings.HasSuffix(s, "\n") {
						rs.leftover = parts[len(parts)-1]
						parts = parts[:len(parts)-1]
					} else {
						rs.leftover = ""
					}
					for _, line := range parts {
						rs.total++
						rs.lines = append(rs.lines, line)
						if len(rs.lines) > tailLinesLocal {
							rs.lines = rs.lines[len(rs.lines)-tailLinesLocal:]
						}
					}
					rs.mu.Unlock()
					showTail()
				}
				if err != nil {
					if err == io.EOF {
						// write leftover as a final line
						rs.mu.Lock()
						if rs.leftover != "" {
							rs.lines = append(rs.lines, rs.leftover)
							if len(rs.lines) > tailLinesLocal {
								rs.lines = rs.lines[len(rs.lines)-tailLinesLocal:]
							}
							rs.leftover = ""
						}
						rs.mu.Unlock()
						showTail()
					}
					break
				}
			}
		}

		go func() {
			defer close(done)
			streamReader(rOut)
		}()
		go func() {
			streamReader(rErr)
		}()

		// prefer Run returning captured output directly
		if opts.Run != nil {
			if out, err := opts.Run(names); err == nil && out != "" {
				// write to temp file and process as stream so full output is available and tail updates
				if rs.file != nil {
					_, _ = rs.file.WriteString(out)
				}
				if logFile != nil {
					_, _ = logFile.WriteString(out)
				}
				// process returned output into tail (simple pass)
				rs.mu.Lock()
				s := rs.leftover + out
				parts := strings.Split(s, "\n")
				if !strings.HasSuffix(s, "\n") {
					rs.leftover = parts[len(parts)-1]
					parts = parts[:len(parts)-1]
				} else {
					rs.leftover = ""
				}
				for _, line := range parts {
					rs.total++
					rs.lines = append(rs.lines, line)
						if len(rs.lines) > tailLinesLocal {
							rs.lines = rs.lines[len(rs.lines)-tailLinesLocal:]
						}
				}
				rs.mu.Unlock()
				showTail()
				logf("Run finished: %v (ok)", names)
				// close pipes and restore fds
				wOut.Close()
				wErr.Close()
				_ = syscall.Dup2(savedOut, outFd)
				_ = syscall.Dup2(savedErr, errFd)
				syscall.Close(savedOut)
				syscall.Close(savedErr)
				return
			}
			// otherwise fall back to fd-based capture and report error status
			err := func() error { _, err := opts.Run(names); return err }()
			// flush and restore
			wOut.Close()
			wErr.Close()
			_ = syscall.Dup2(savedOut, outFd)
			_ = syscall.Dup2(savedErr, errFd)
			syscall.Close(savedOut)
			syscall.Close(savedErr)
			footer := fmt.Sprintf("--- END RUN: %s (err=%v) ---\n", strings.Join(names, ","), err)
			if rs.file != nil {
				_, _ = rs.file.WriteString(footer)
			}
			if logFile != nil {
				_, _ = logFile.WriteString(footer)
			}
			app.QueueUpdateDraw(func() { output.Write([]byte(footer)) })
			logf("Run finished: %v (err=%v)", names, err)
			return
		}
	}

	tree.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		// handle special keys first
		if ev.Key() == tcell.KeyTAB {
			if focusedOnTree {
				app.SetFocus(preview)
			} else {
				app.SetFocus(tree)
			}
			focusedOnTree = !focusedOnTree
			return nil
		}
		switch ev.Rune() {
		case ' ':
			node := tree.GetCurrentNode()
			if node == nil {
			 return nil
			}
			ref := node.GetReference()
			if ref == nil {
			 return nil
			}
			if tp, ok := ref.(*task.Task); ok {
				selected[tp.Name] = !selected[tp.Name]
				buildTree()
			}
			return nil
		case 'u':
			prompt(app, "Under path (A/B/C): ", func(val string) { under = val; buildTree() })
			return nil
		case 't':
			prompt(app, "Tags CSV: ", func(val string) { tags = val; buildTree() })
			return nil
		case 'r':
			var names []string
			for n, on := range selected {
				if on {
					names = append(names, n)
				}
			}
			// if nothing selected, try to run the currently focused node (if it's a task)
			if len(names) == 0 {
				if node := tree.GetCurrentNode(); node != nil {
					if ref := node.GetReference(); ref != nil {
						if tp, ok := ref.(*task.Task); ok {
							names = append(names, tp.Name)
						}
					}
				}
			}
			if opts.Run != nil {
				// run in goroutine and capture output
				go captureRun(names)
			}
			return nil
		case 'q':
			app.Stop()
			return nil
		case 'o':
			// open full output modal if available
			currentRunMu.Lock()
			rs := currentRun
			currentRunMu.Unlock()
			if rs == nil || rs.tmpPath == "" {
				return nil
			}
			// read file content
			data, _ := os.ReadFile(rs.tmpPath)
			text := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
			text.SetBorder(true).SetTitle("Full Output")
			text.SetText(string(data))
			text.SetScrollable(true)
			// modal: use a Flex to allow esc to close
			modal := tview.NewFlex().SetDirection(tview.FlexRow).
				AddItem(text, 0, 1, true).
				AddItem(tview.NewTextView().SetText("Press Esc to close"), 1, 0, false)
			// capture Esc to return to main layout
			prevLayout := layout
			// save old input capture
			oldCapture := app.GetInputCapture()
			app.SetRoot(modal, true).SetFocus(modal)
			app.SetInputCapture(func(ev2 *tcell.EventKey) *tcell.EventKey {
				if ev2.Key() == tcell.KeyEsc {
					app.SetRoot(prevLayout, true).SetFocus(tree)
					app.SetInputCapture(oldCapture)
					return nil
				}
				return ev2
			})
			return nil
		case 'e':
			// expand all
			if tree.GetRoot() != nil {
				expandAll(tree.GetRoot())
			}
			return nil
		case 'c':
			if tree.GetRoot() != nil {
				collapseAll(tree.GetRoot())
			}
			return nil
		}
		return ev
	})

	// Left: tree, Right: preview
	// layout: info + help on top, then columns (tree/preview), then output box
	cols := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(tree, 0, 2, true).
		AddItem(preview, 0, 3, false)
	top := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(info, 1, 0, false).
		AddItem(help, 1, 0, false)
	mainArea := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(cols, 0, 1, true).
		AddItem(output, 10, 0, false)
	layout = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(top, 3, 0, false).
		AddItem(mainArea, 0, 1, true)

	return app.SetRoot(layout, true).Run()
}

func prompt(app *tview.Application, label string, cb func(string)) {
	input := tview.NewInputField().SetLabel(label)
	form := tview.NewForm().AddFormItem(input).AddButton("OK", func() {
		cb(input.GetText())
	}).AddButton("Cancel", func() { cb("") })
	form.SetBorder(true).SetTitle("Input").SetTitleAlign(tview.AlignLeft)
	app.SetRoot(form, true).SetFocus(form)
}

func split(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if q := strings.TrimSpace(p); q != "" {
			out = append(out, q)
		}
	}
	return out
}

// extractHeadingText returns the non-code text under the given heading path.
// It parses the file line-by-line, matching markdown headings and capturing
// the section until the next heading of same or higher level. Code fences
// are skipped from the captured output.
func extractHeadingText(path string, heading []string) string {
	if path == "" || len(heading) == 0 {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	// find the starting heading and its level
	target := heading
	inCode := false
	codeFence := ""
	cur := []string{}
	found := false
	level := 0
	// helper to check heading line
	isHeading := func(s string) (int, string, bool) {
		s = strings.TrimRight(s, "\r")
		if !strings.HasPrefix(s, "#") {
			return 0, "", false
		}
		i := 0
		for i < len(s) && s[i] == '#' {
			i++
		}
		if i == 0 || i >= len(s) || s[i] != ' ' {
			return 0, "", false
		}
		text := strings.TrimSpace(s[i+1:])
		return i, text, true
	}
	// simple fence detection
	fenceStart := func(s string) (string, bool) {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "```") {
			// language after ticks optional
			tok := strings.TrimSpace(strings.TrimPrefix(s, "```"))
			if tok == "" {
				tok = "code"
			}
			return tok, true
		}
		return "", false
	}
	fenceEnd := func(s string) bool { return strings.TrimSpace(s) == "```" }

	// scan
	pathStack := []string{}
	for _, l := range lines {
		if !inCode {
			if lev, text, ok := isHeading(l); ok {
				// adjust pathStack to this level
				for len(pathStack) >= lev {
					pathStack = pathStack[:len(pathStack)-1]
				}
				pathStack = append(pathStack, text)
				if !found {
					if len(pathStack) == len(target) && strings.EqualFold(strings.Join(pathStack, "/"), strings.Join(target, "/")) {
						found = true
						level = lev
						continue
					}
				} else {
					// found section ended when a heading of same or higher level appears
					if lev <= level {
						break
					}
				}
				continue
			}
			if found {
				if lang, ok := fenceStart(l); ok {
					inCode = true
					codeFence = lang
					_ = codeFence
					continue
				}
				// collect non-empty text lines
				cur = append(cur, l)
			}
		} else {
			if fenceEnd(l) {
				inCode = false
				codeFence = ""
				continue
			}
			// skip code content
		}
	}
	// trim trailing/leading blank lines
	i := 0
	for i < len(cur) && strings.TrimSpace(cur[i]) == "" {
		i++
	}
	j := len(cur)
	for j > i && strings.TrimSpace(cur[j-1]) == "" {
		j--
	}
	cur = cur[i:j]
	return strings.Join(cur, "\n")
}
