package markdown

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/SimonWaldherr/mdexec/internal/task"
)

var (
	fenceStart = regexp.MustCompile("^```(\\S+)(?:\\s+(.*))?$")
	fenceEnd   = regexp.MustCompile("^```\\s*$")
	headingRe  = regexp.MustCompile("^(#{1,6})\\s+(.*)$")
	slugRe     = regexp.MustCompile(`[^a-z0-9]+`)
)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "unnamed"
	}
	return s
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		if q := strings.TrimSpace(p); q != "" {
			out = append(out, q)
		}
	}
	return out
}

// parse tokens in fence info string, with simple quote support
func parseInfoTokens(info string) []string {
	var tokens []string
	var cur strings.Builder
	inQuote := false
	var quote rune
	esc := false
	for _, c := range info {
		if esc {
			cur.WriteRune(c)
			esc = false
			continue
		}
		if c == '\\' {
			esc = true
			continue
		}
		if inQuote {
			if c == quote {
				inQuote = false
			} else {
				cur.WriteRune(c)
			}
			continue
		}
		if c == '\'' || c == '"' {
			inQuote = true
			quote = c
			continue
		}
		if c == ' ' || c == '\t' {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(c)
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

func parseKVs(tokens []string) map[string]string {
	out := map[string]string{}
	for _, t := range tokens {
		if !strings.Contains(t, "=") {
			continue
		}
		parts := strings.SplitN(t, "=", 2)
		k := strings.TrimSpace(parts[0])
		v := strings.TrimSpace(parts[1])
		// strip quotes
		if (strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"")) ||
			(strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'")) {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out
}

func ParseMarkdown(path string, includeAll bool) ([]task.Task, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var tasks []task.Task
	type head struct {
		level int
		text  string
	}
	var stack []head
	sc := bufio.NewScanner(f)
	inCode := false
	codeLang := ""
	infoTokens := []string{}
	codeLines := []string{}
	taskMeta := map[string]string{}
	mdexec := false
	start := 0

	lineno := 0
	for sc.Scan() {
		lineno++
		line := sc.Text()
		if !inCode {
			if m := headingRe.FindStringSubmatch(line); m != nil {
				level := len(m[1])
				text := strings.TrimSpace(m[2])
				for len(stack) > 0 && stack[len(stack)-1].level >= level {
					stack = stack[:len(stack)-1]
				}
				stack = append(stack, head{level, text})
				continue
			}
			if m := fenceStart.FindStringSubmatch(line); m != nil {
				codeLang = strings.ToLower(strings.TrimSpace(m[1]))
				info := strings.TrimSpace(m[2])
				if info != "" {
					infoTokens = parseInfoTokens(info)
				} else {
					infoTokens = nil
				}
				mdexec = false
				for _, t := range infoTokens {
					if strings.ToLower(t) == "mdexec" {
						mdexec = true
						break
					}
				}
				taskMeta = parseKVs(infoTokens)
				codeLines = nil
				inCode = true
				start = lineno + 1
				continue
			}
		} else {
			if fenceEnd.MatchString(line) {
				shouldCreate := mdexec || includeAll
				if shouldCreate {
					name := taskMeta["name"]
					if name == "" {
						last := "root"
						if len(stack) > 0 {
							last = stack[len(stack)-1].text
						}
						firstLine := ""
						for _, cl := range codeLines {
							if strings.TrimSpace(cl) != "" {
								firstLine = cl
								break
							}
						}
						derived := last + "-" + firstLine
						name = slugify(derived)
					}
					deps := splitCSV(taskMeta["deps"])
					tags := splitCSV(taskMeta["tags"])
					dir := taskMeta["dir"]
					shell := taskMeta["shell"]
					cont := strings.EqualFold(taskMeta["continue"], "true") ||
						taskMeta["continue"] == "1" || strings.EqualFold(taskMeta["continue"], "yes") ||
						strings.EqualFold(taskMeta["continue"], "on")
					timeout := 0
					if ts := strings.TrimSpace(taskMeta["timeout"]); ts != "" {
						if n, err := strconv.Atoi(ts); err == nil && n > 0 {
							timeout = n
						}
					}
					var heading []string
					for _, h := range stack {
						heading = append(heading, h.text)
					}
					t := task.Task{
						ID:        fmt.Sprintf("t%d", len(tasks)+1),
						Name:      name,
						Lang:      codeLang,
						Code:      strings.Join(codeLines, "\n"),
						Heading:   heading,
						Deps:      deps,
						Tags:      tags,
						Dir:       dir,
						Shell:     shell,
						Continue:  cont,
						Timeout:   timeout,
						StartLine: start,
						EndLine:   lineno - 1,
						File:      path,
						Info:      taskMeta,
						Marked:    mdexec,
					}
					tasks = append(tasks, t)
				}
				inCode = false
				codeLang = ""
				infoTokens = nil
				codeLines = nil
				taskMeta = map[string]string{}
				mdexec = false
				start = 0
				continue
			} else {
				codeLines = append(codeLines, line)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// ensure unique names
	seen := map[string]int{}
	for i := range tasks {
		n := tasks[i].Name
		if c, ok := seen[n]; ok {
			seen[n] = c + 1
			tasks[i].Name = fmt.Sprintf("%s-%d", n, c+1)
		} else {
			seen[n] = 1
		}
	}
	return tasks, nil
}
