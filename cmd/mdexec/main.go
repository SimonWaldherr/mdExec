package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/SimonWaldherr/mdexec/internal/aisafety"
	"github.com/SimonWaldherr/mdexec/internal/executor"
	"github.com/SimonWaldherr/mdexec/internal/graph"
	"github.com/SimonWaldherr/mdexec/internal/markdown"
	"github.com/SimonWaldherr/mdexec/internal/policy"
	"github.com/SimonWaldherr/mdexec/internal/task"
	"github.com/SimonWaldherr/mdexec/internal/ui"
	"github.com/spf13/cobra"
)

var (
	flagIncludeAll   bool
	flagYes          bool
	flagDryRun       bool
	flagVerbosity    int
	flagEngine       string
	flagImage        string
	flagUnder        string
	flagLang         string
	flagTags         string
	flagNames        string
	flagMatch        string
	flagTimeout      int
	flagAllow        string
	flagPolicy       string
	flagUseContainer bool
	flagPassEnv      []string
	flagVars         []string
	flagTail         int
	flagAISafety     bool
	flagAIProvider   string
	flagAIURL        string
	flagAIModel      string
	flagAITimeout    int
)

func main() {
	root := &cobra.Command{
		Use:   "mdexec",
		Short: "Execute runnable code blocks from Markdown",
		Long:  "Parse Markdown headings, discover runnable code fences, preview, plan, and execute with simple policy and containerization.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// if neither --yes nor --dry-run set: default to dry-run
			if !flagYes && !flagDryRun {
				flagDryRun = true
			}
			flagAllow = strings.ToLower(flagAllow)
			if flagAllow == "" {
				flagAllow = "low"
			}
			if flagAIURL == "" {
				flagAIURL = os.Getenv("MDEXEC_AI_URL")
			}
			if flagAIModel == "" {
				flagAIModel = os.Getenv("MDEXEC_AI_MODEL")
			}
			return nil
		},
	}
	root.PersistentFlags().BoolVar(&flagIncludeAll, "include-all", false, "Treat all fenced blocks in whitelisted languages as candidates (even without mdexec tag)")
	root.PersistentFlags().BoolVarP(&flagYes, "yes", "y", false, "Actually execute (otherwise dry-run)")
	root.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "Dry-run (preview mode)")
	root.PersistentFlags().IntVarP(&flagVerbosity, "verbosity", "v", 1, "Verbosity 0..3")
	root.PersistentFlags().StringVar(&flagEngine, "engine", "", "Container engine (docker|podman)")
	root.PersistentFlags().StringVar(&flagImage, "container", "", "Container image to run inside")
	root.PersistentFlags().BoolVar(&flagUseContainer, "use-container", false, "Prefer running inside containers using policy defaults")
	root.PersistentFlags().StringSliceVar(&flagPassEnv, "pass-env", []string{"CI", "HOME"}, "Env vars to pass into container")
	root.PersistentFlags().StringSliceVar(&flagVars, "var", nil, "Set variable KEY=VALUE (replaces {{KEY}} and exports to env)")
	root.PersistentFlags().IntVar(&flagTail, "tail", 5, "Number of lines to show in TUI output tail")
	root.PersistentFlags().BoolVar(&flagAISafety, "ai-safety", false, "Enable AI safety check to approve/block each task before execution using a local AI model")
	root.PersistentFlags().StringVar(&flagAIProvider, "ai-provider", "auto", "Local AI API provider (auto|ollama|openai)")
	root.PersistentFlags().StringVar(&flagAIURL, "ai-url", "", "Local AI HTTP endpoint (MDEXEC_AI_URL or empty for Ollama http://localhost:11434/api/chat)")
	root.PersistentFlags().StringVar(&flagAIModel, "ai-model", "", "Local AI model name (or MDEXEC_AI_MODEL)")
	root.PersistentFlags().IntVar(&flagAITimeout, "ai-timeout", 30, "AI safety request timeout seconds")

	// Common filter flags
	addFilterFlags := func(c *cobra.Command) {
		c.Flags().StringVar(&flagUnder, "under", "", "Limit to tasks under heading path, e.g., 'Setup/Install'")
		c.Flags().StringVar(&flagLang, "lang", "", "Filter by language")
		c.Flags().StringVar(&flagTags, "tags", "", "Filter by comma-separated tags")
		c.Flags().StringVar(&flagNames, "names", "", "Limit to specific task names (comma-separated)")
		c.Flags().StringVar(&flagMatch, "match", "", "Regex to match task name or path")
		c.Flags().IntVar(&flagTimeout, "timeout", 0, "Default timeout seconds")
		c.Flags().StringVar(&flagAllow, "allow", "", "Allow execution up to risk level (low|medium|high)")
		c.Flags().StringVar(&flagPolicy, "policy", "", "Path to mdexec.policy.yaml (optional)")
	}

	listCmd := &cobra.Command{
		Use:   "list <markdown>",
		Short: "List runnable tasks",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			md := args[0]
			return withIndex(md, func(idx *task.Index, pol *policy.Policy) error {
				selection, err := selectTasks(idx, pol)
				if err != nil {
					return err
				}
				task.SortByPathThenName(selection)
				fmt.Print(task.FormatTable(selection))
				return nil
			})
		},
	}
	addFilterFlags(listCmd)
	root.AddCommand(listCmd)

	showCmd := &cobra.Command{
		Use:   "show <markdown> --name <task>",
		Short: "Show code of a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			md := args[0]
			name, _ := cmd.Flags().GetString("name")
			if name == "" {
				return fmt.Errorf("--name required")
			}
			return withIndex(md, func(idx *task.Index, pol *policy.Policy) error {
				t, ok := idx.ByName[name]
				if !ok {
					return fmt.Errorf("task '%s' not found", name)
				}
				header := "```" + t.Lang + " mdexec"
				// rebuild info string (approximate)
				var parts []string
				for k, v := range t.Info {
					if strings.ToLower(k) == "mdexec" {
						continue
					}
					parts = append(parts, fmt.Sprintf("%s=%s", k, v))
				}
				if len(parts) > 0 {
					header = "```" + t.Lang + " " + strings.Join(parts, " ") + " mdexec"
				}
				fmt.Println(header)
				fmt.Println(t.Code)
				fmt.Println("```")
				return nil
			})
		},
	}
	showCmd.Flags().String("name", "", "Task name to show")
	addFilterFlags(showCmd)
	root.AddCommand(showCmd)

	planCmd := &cobra.Command{
		Use:   "plan <markdown>",
		Short: "Show execution plan (topo order incl. deps)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			md := args[0]
			graphOut, _ := cmd.Flags().GetString("graph")
			return withIndex(md, func(idx *task.Index, pol *policy.Policy) error {
				selection, err := selectTasks(idx, pol)
				if err != nil {
					return err
				}
				order, err := idx.TopoSort(selection)
				if err != nil {
					return err
				}
				for i, t := range order {
					fmt.Printf("%02d. %s  deps=[%s]\n", i+1, t.Label(), strings.Join(t.Deps, ", "))
				}
				if graphOut != "" {
					if err := graph.WriteDOT(order, graphOut); err != nil {
						return err
					}
					fmt.Println("Wrote graph to:", graphOut)
				}
				return nil
			})
		},
	}
	planCmd.Flags().String("graph", "", "Write DOT graph to this path")
	addFilterFlags(planCmd)
	root.AddCommand(planCmd)

	runCmd := &cobra.Command{
		Use:   "run <markdown>",
		Short: "Execute tasks (dry-run by default)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			md := args[0]
			return withIndex(md, func(idx *task.Index, pol *policy.Policy) error {
				selection, err := selectTasks(idx, pol)
				if err != nil {
					return err
				}
				order, err := idx.TopoSort(selection)
				if err != nil {
					return err
				}
				vars := parseVars(flagVars)
				opts := executor.Options{
					Timeout:      time.Duration(flagTimeout) * time.Second,
					Engine:       flagEngine,
					Image:        flagImage,
					PassEnv:      flagPassEnv,
					DryRun:       flagDryRun,
					Verbosity:    flagVerbosity,
					AllowLevel:   parseAllow(flagAllow),
					IncludeAll:   flagIncludeAll,
					UseContainer: flagUseContainer || flagImage != "" || flagEngine != "",
				}
				aiOpts := aiSafetyOptions()
				ctx := context.Background()
				failures, err := executeTaskOrder(ctx, order, executionConfig{
					Policy: pol,
					Vars:   vars,
					Run:    opts,
					AI:     aiOpts,
					Yes:    flagYes,
					Out:    os.Stdout,
					ErrOut: os.Stderr,
				})
				if err != nil {
					return err
				}
				if opts.DryRun {
					fmt.Println("Dry-run complete. Use --yes to execute.")
				} else if failures > 0 {
					fmt.Fprintf(os.Stderr, "Run finished with %d failure(s).\n", failures)
				} else {
					fmt.Println("All tasks completed successfully.")
				}
				return nil
			})
		},
	}
	addFilterFlags(runCmd)
	root.AddCommand(runCmd)

	tuiCmd := &cobra.Command{
		Use:   "tui <markdown>",
		Short: "Interactive TUI (requires tview/tcell)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			md := args[0]
			var idx *task.Index
			var pol *policy.Policy
			var err error
			if err = withIndex(md, func(i *task.Index, p *policy.Policy) error {
				idx, pol = i, p
				return nil
			}); err != nil {
				return err
			}
			listFn := func() []task.Task {
				selection, _ := selectTasks(idx, pol)
				return selection
			}
			// Helper that runs specified task names and returns combined output.
			runAndCapture := func(names []string) (string, error) {
				flagNames = strings.Join(names, ",")
				// when invoked from TUI, explicitly run (not dry-run)
				prevYes, prevDry := flagYes, flagDryRun
				flagYes = true
				flagDryRun = false
				defer func() { flagYes, flagDryRun = prevYes, prevDry }()

				var outBuilder strings.Builder
				err := withIndex(md, func(idx *task.Index, pol *policy.Policy) error {
					selection, err := selectTasks(idx, pol)
					if err != nil {
						return err
					}
					order, err := idx.TopoSort(selection)
					if err != nil {
						return err
					}
					vars := parseVars(flagVars)
					opts := executor.Options{
						Timeout:      time.Duration(flagTimeout) * time.Second,
						Engine:       flagEngine,
						Image:        flagImage,
						PassEnv:      flagPassEnv,
						DryRun:       flagDryRun,
						Verbosity:    flagVerbosity,
						AllowLevel:   parseAllow(flagAllow),
						IncludeAll:   flagIncludeAll,
						UseContainer: flagUseContainer || flagImage != "" || flagEngine != "",
					}
					aiOpts := aiSafetyOptions()
					failures, err := executeTaskOrder(context.Background(), order, executionConfig{
						Policy: pol,
						Vars:   vars,
						Run:    opts,
						AI:     aiOpts,
						Yes:    true,
						Out:    &outBuilder,
						ErrOut: &outBuilder,
					})
					if err != nil {
						return err
					}
					if opts.DryRun {
						outBuilder.WriteString("Dry-run complete. Use --yes to execute.\n")
					} else if failures > 0 {
						outBuilder.WriteString(fmt.Sprintf("Run finished with %d failure(s).\n", failures))
					} else {
						outBuilder.WriteString("All tasks completed successfully.\n")
					}
					return nil
				})
				return outBuilder.String(), err
			}
			filterFn := func(under string, tags []string) []task.Task {
				flagUnder = under
				flagTags = strings.Join(tags, ",")
				selection, _ := selectTasks(idx, pol)
				return selection
			}
			return ui.RunTUI(listFn, ui.TUIOptions{Run: runAndCapture, Filter: filterFn, TailLines: flagTail})
		},
	}
	addFilterFlags(tuiCmd)
	root.AddCommand(tuiCmd)

	configCmd := &cobra.Command{
		Use:   "config <markdown>",
		Short: "Show loaded policy/config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			md := args[0]
			pol, err := policy.Load(md, defaultPolicyPath())
			if err != nil {
				return err
			}
			fmt.Printf("Policy: %+v\n", *pol)
			return nil
		},
	}
	root.AddCommand(configCmd)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func withIndex(md string, fn func(*task.Index, *policy.Policy) error) error {
	abs, _ := filepath.Abs(md)
	pol, err := policy.Load(abs, defaultPolicyPath())
	if err != nil {
		return err
	}
	// If the input filename indicates a 'nomarker' sample (e.g. README.nomarker.md),
	// treat it as if --include-all was passed so code fences without an explicit
	// mdexec tag are considered runnable.
	includeAll := flagIncludeAll
	if strings.Contains(strings.ToLower(filepath.Base(abs)), ".nomarker") {
		includeAll = true
	}
	tasks, err := markdown.ParseMarkdown(abs, includeAll)
	if err != nil {
		return err
	}
	// Filter by whitelist
	var filtered []task.Task
	for _, t := range tasks {
		if pol.IsWhitelisted(t.Lang) {
			filtered = append(filtered, t)
		}
	}
	idx := task.NewIndex(filtered)
	return fn(idx, pol)
}

func selectTasks(idx *task.Index, pol *policy.Policy) ([]task.Task, error) {
	var names []string
	if flagNames != "" {
		names = splitCSV(flagNames)
	}
	var tags []string
	if flagTags != "" {
		tags = splitCSV(flagTags)
	}
	var rx *regexp.Regexp
	if flagMatch != "" {
		rx = regexp.MustCompile(flagMatch)
	}
	selection := idx.Filter(task.Filter{
		Names: names,
		Under: flagUnder,
		Lang:  flagLang,
		Tags:  tags,
		Regex: rx,
	})
	return selection, nil
}

func parseVars(kvs []string) map[string]string {
	out := map[string]string{}
	for _, kv := range kvs {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		out[parts[0]] = parts[1]
	}
	return out
}

func splitCSV(s string) []string {
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

func parseAllow(s string) policy.RiskLevel {
	switch strings.ToLower(s) {
	case "high":
		return policy.RiskHigh
	case "medium":
		return policy.RiskMedium
	default:
		return policy.RiskLow
	}
}

func aiSafetyOptions() aisafety.Options {
	return aisafety.Options{
		Enabled:  flagAISafety,
		Provider: flagAIProvider,
		Endpoint: flagAIURL,
		Model:    flagAIModel,
		Timeout:  time.Duration(flagAITimeout) * time.Second,
	}
}

func isAllowed(risk, allow policy.RiskLevel) bool {
	order := map[policy.RiskLevel]int{policy.RiskLow: 1, policy.RiskMedium: 2, policy.RiskHigh: 3}
	return order[risk] <= order[allow]
}

type executionConfig struct {
	Policy *policy.Policy
	Vars   map[string]string
	Run    executor.Options
	AI     aisafety.Options
	Yes    bool
	Out    io.Writer
	ErrOut io.Writer
}

// executeTaskOrder runs tasks in the given order, writing stdout to out and stderr to errOut.
// It returns the number of tasks that exited non-zero.
func executeTaskOrder(ctx context.Context, order []*task.Task, cfg executionConfig) (int, error) {
	failures := 0
	for _, t := range order {
		score, reasons := cfg.Policy.Score(t.Code)
		t.RiskScore = score
		t.RiskReasons = reasons
		risk := cfg.Policy.Risk(score)
		if !isAllowed(risk, cfg.Run.AllowLevel) {
			if cfg.Yes {
				return failures, fmt.Errorf("task '%s' risk %s exceeds allowed level '%s' (use --allow %s)", t.Name, risk, cfg.Run.AllowLevel, risk)
			}
			fmt.Fprintf(cfg.Out, "[blocked] %s: risk %s (%v). Increase --allow or run with --yes.\n", t.Label(), risk, strings.Join(reasons, "; "))
			continue
		}
		if cfg.AI.Enabled {
			decision, err := aisafety.Check(ctx, cfg.AI, aisafety.Request{TaskName: t.Name, Language: t.Lang, Code: t.Code})
			if err != nil {
				if cfg.Yes {
					return failures, fmt.Errorf("task '%s' AI safety check failed: %w", t.Name, err)
				}
				fmt.Fprintf(cfg.Out, "[ai-error] %s: %v\n", t.Label(), err)
				continue
			}
			if !decision.Safe {
				if cfg.Yes {
					return failures, fmt.Errorf("task '%s' blocked by AI safety check: %s", t.Name, decision.Reason)
				}
				fmt.Fprintf(cfg.Out, "[ai-blocked] %s: %s\n", t.Label(), decision.Reason)
				continue
			}
			if cfg.Run.Verbosity > 0 {
				fmt.Fprintf(cfg.Out, "[ai-safe] %s: %s\n", t.Label(), decision.Reason)
			}
		}
		rc, stdout, stderr, err := executor.RunTask(ctx, t, cfg.Policy, cfg.Vars, cfg.Run)
		if err != nil {
			return failures, err
		}
		if len(stdout) > 0 {
			fmt.Fprint(cfg.Out, stdout)
		}
		if len(stderr) > 0 {
			fmt.Fprint(cfg.ErrOut, stderr)
		}
		if rc != 0 {
			failures++
			if !t.Continue {
				fmt.Fprintf(cfg.ErrOut, "[stop] task '%s' failed with exit code %d\n", t.Name, rc)
				break
			}
		}
	}
	return failures, nil
}

func defaultPolicyPath() string {
	wd, _ := os.Getwd()
	return filepath.Join(wd, "policy", "mdexec.policy.yaml")
}
