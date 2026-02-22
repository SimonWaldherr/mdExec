package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/SimonWaldherr/mdexec/internal/policy"
	"github.com/SimonWaldherr/mdexec/internal/task"
)

type Runner struct {
	Mode    string   // "stdin" | "file"
	Cmd     []string // base command; may include "{file}"
	FileExt string
	Detect  []string
}

func defaultRunners() map[string]Runner {
	return map[string]Runner{
		"bash":   {Mode: "stdin", Cmd: []string{"/bin/bash", "-s"}},
		"sh":     {Mode: "stdin", Cmd: []string{"/bin/sh", "-s"}},
		"python": {Mode: "stdin", Cmd: []string{"python3", "-"}},
		"node":   {Mode: "stdin", Cmd: []string{"node", "-"}, Detect: []string{"node"}},
		"deno":   {Mode: "stdin", Cmd: []string{"deno", "run", "-"}, Detect: []string{"deno"}},
		"ruby":   {Mode: "stdin", Cmd: []string{"ruby", "-"}, Detect: []string{"ruby"}},
		"perl":   {Mode: "stdin", Cmd: []string{"perl", "-"}, Detect: []string{"perl"}},
		"php":    {Mode: "file", Cmd: []string{"php", "{file}"}, FileExt: ".php", Detect: []string{"php"}},
		"go":     {Mode: "file", Cmd: []string{"go", "run", "{file}"}, FileExt: ".go", Detect: []string{"go"}},
		"pwsh":   {Mode: "file", Cmd: []string{"pwsh", "-NoLogo", "-NoProfile", "-File", "{file}"}, FileExt: ".ps1", Detect: []string{"pwsh", "powershell"}},
		"make":   {Mode: "file", Cmd: []string{"make", "-f", "{file}"}, FileExt: ".mk", Detect: []string{"make"}},
	}
}

type Options struct {
	Timeout      time.Duration
	Engine       string
	Image        string
	PassEnv      []string
	DryRun       bool
	Verbosity    int // 0..3
	AllowLevel   policy.RiskLevel
	IncludeAll   bool
	UseContainer bool
}

var varRe = regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}`)

func replaceVars(s string, vars map[string]string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		key := varRe.FindStringSubmatch(m)[1]
		if val, ok := vars[key]; ok {
			return val
		}
		if val := os.Getenv(key); val != "" {
			return val
		}
		return m
	})
}

func ensureTmpFile(workdir, ext string) (string, error) {
	dir := filepath.Join(workdir, ".mdexec", "tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "task-*"+ext)
	if err != nil {
		return "", err
	}
	path := f.Name()
	_ = f.Close()
	return path, nil
}

func lookPathAny(exes []string) string {
	for _, e := range exes {
		if p, err := exec.LookPath(e); err == nil {
			return p
		}
	}
	return ""
}

func RunTask(ctx context.Context, t *task.Task, pol *policy.Policy, vars map[string]string, opts Options) (int, string, string, error) {
	code := replaceVars(t.Code, vars)
	var cwd string
	// If task specifies a dir, respect it. If it's relative, interpret it relative
	// to the markdown file's directory so code blocks can use paths relative to
	// the source document. If no dir specified, default to the markdown file's
	// directory; fallback to the current working directory.
	if t.Dir != "" {
		if filepath.IsAbs(t.Dir) {
			cwd = t.Dir
		} else {
			if t.File != "" {
				cwd = filepath.Join(filepath.Dir(t.File), t.Dir)
			} else {
				cwd = t.Dir
			}
		}
	} else {
		if t.File != "" {
			cwd = filepath.Dir(t.File)
		} else {
			cwd, _ = os.Getwd()
		}
	}
	env := os.Environ()
	for k, v := range vars {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	runners := defaultRunners()
	r, ok := runners[t.Lang]
	if !ok {
		if opts.Verbosity > 0 {
			fmt.Fprintf(os.Stderr, "[skip] unsupported language '%s' (task %s)\n", t.Lang, t.Name)
		}
		return 0, "", "", nil
	}
	cmd := append([]string{}, r.Cmd...)
	useStdin := r.Mode == "stdin"
	var codeFile string

	// Shell override
	if (t.Lang == "bash" || t.Lang == "sh") && t.Shell != "" {
		if useStdin {
			cmd = []string{t.Shell, "-s"}
		} else {
			cmd = []string{t.Shell, "-c"}
		}
	}

	if !useStdin {
		ext := r.FileExt
		if ext == "" {
			ext = "." + t.Lang
		}
		f, err := ensureTmpFile(cwd, ext)
		if err != nil {
			return 1, "", "", err
		}
		codeFile = f
		if err := os.WriteFile(codeFile, []byte(code), 0o644); err != nil {
			return 1, "", "", err
		}
		for i := range cmd {
			cmd[i] = strings.ReplaceAll(cmd[i], "{file}", codeFile)
		}
	}

	// container wrapper
	final := cmd
	engine := opts.Engine
	image := opts.Image
	if opts.UseContainer {
		if engine == "" {
			engine = detectEngine(pol.DefaultSandbox.Engine)
		}
		if image == "" {
			image = pol.DefaultSandbox.Image
		}
	}
	pass := map[string]string{}
	for _, k := range opts.PassEnv {
		if v := os.Getenv(k); v != "" {
			pass[k] = v
		}
	}
	if w := wrapContainer(engine, image, cwd, cmd, useStdin, pol.DefaultSandbox, pass); w != nil {
		final = w
	}

	timeout := opts.Timeout
	if t.Timeout > 0 {
		timeout = time.Duration(t.Timeout) * time.Second
	}
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if opts.DryRun {
		fmt.Printf("--- DRY RUN: %s ---\n", t.Label())
		fmt.Println(code)
		fmt.Println("--- END ---")
		return 0, "", "", nil
	}

	// runtime check
	if len(r.Detect) > 0 && image == "" {
		if lookPathAny(r.Detect) == "" && opts.Verbosity > 0 {
			fmt.Fprintf(os.Stderr, "[warn] missing runtime for language '%s' (task %s). Consider --use-container or install runtime.\n", t.Lang, t.Name)
		}
	}

	if opts.Verbosity > 0 {
		fmt.Printf("=== RUN: %s ===\n", t.Label())
	}
	command := exec.CommandContext(ctx, final[0], final[1:]...)
	command.Env = env
	command.Dir = cwd
	if useStdin {
		command.Stdin = bytes.NewBufferString(code)
	}
	var outBuf, errBuf bytes.Buffer
	command.Stdout = &outBuf
	command.Stderr = &errBuf
	err := command.Run()
	if ctx.Err() == context.DeadlineExceeded {
		fmt.Fprintf(os.Stderr, "[error] task '%s' timed out after %s\n", t.Name, timeout.String())
		return 124, "", "", nil
	}
	if err != nil {
		// return captured output and exit code
		outStr, errStr := outBuf.String(), errBuf.String()
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), outStr, errStr, nil
		}
		return 1, outStr, errStr, err
	}
	// on success, return captured output
	return 0, outBuf.String(), errBuf.String(), nil
}
