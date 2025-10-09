package policy

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type Rule struct {
	Pattern string `yaml:"pattern" json:"pattern"`
	Score   int    `yaml:"score" json:"score"`
	Reason  string `yaml:"reason" json:"reason"`

	re *regexp.Regexp
}

type Thresholds struct {
	LowMax int `yaml:"low_max" json:"low_max"`
	MedMax int `yaml:"med_max" json:"med_max"`
	HighMin int `yaml:"high_min" json:"high_min"`
}

type Sandbox struct {
	Engine      string `yaml:"engine" json:"engine"`   // docker|podman
	Image       string `yaml:"image" json:"image"`
	Network     string `yaml:"network" json:"network"` // none|restricted|full
	WorkspaceRW bool   `yaml:"workspace_rw" json:"workspace_rw"`
}

type Policy struct {
	WhitelistLanguages []string `yaml:"whitelist_languages" json:"whitelist_languages"`
	BlacklistPatterns  []Rule   `yaml:"blacklist_patterns" json:"blacklist_patterns"`
	Thresholds         Thresholds `yaml:"thresholds" json:"thresholds"`
	DefaultSandbox     Sandbox   `yaml:"default_sandbox" json:"default_sandbox"`
	ConfirmFor         string    `yaml:"confirm_for" json:"confirm_for"` // "high"|"medium"|""
}

// Load policy by walking up from the markdown path; fallback to provided defaultPath; else built-in defaults.
func Load(markdownPath string, defaultPath string) (*Policy, error) {
	// search upwards for mdexec.policy.yaml
	dir := filepath.Dir(markdownPath)
	var cand string
	for {
		p := filepath.Join(dir, "mdexec.policy.yaml")
		if _, err := os.Stat(p); err == nil {
			cand = p
			break
		}
		nd := filepath.Dir(dir)
		if nd == dir {
			break
		}
		dir = nd
	}
	if cand == "" && defaultPath != "" {
		if _, err := os.Stat(defaultPath); err == nil {
			cand = defaultPath
		}
	}
	pol := Default()
	if cand != "" {
		b, err := os.ReadFile(cand)
		if err == nil {
			_ = yaml.Unmarshal(b, pol)
		}
	}
	// compile regex
	for i := range pol.BlacklistPatterns {
		rx := pol.BlacklistPatterns[i].Pattern
		if rx == "" {
			continue
		}
		// best-effort compile; skip on error
		if re, err := regexp.Compile(rx); err == nil {
			pol.BlacklistPatterns[i].re = re
		}
	}
	return pol, nil
}

func Default() *Policy {
	return &Policy{
		WhitelistLanguages: []string{"bash","sh","python","node","deno","go","php","ruby","perl","pwsh","make"},
		BlacklistPatterns: []Rule{
			{Pattern: "\\brm\\s+-rf\\s+/", Score: 9, Reason: "Destructive recursive delete of root"},
			{Pattern: "curl\\s+.*\\|\\s*sh", Score: 9, Reason: "Piping remote script to shell"},
			{Pattern: "\\bsudo\\b", Score: 5, Reason: "Elevated privileges"},
		},
		Thresholds: Thresholds{LowMax: 2, MedMax: 5, HighMin: 6},
		DefaultSandbox: Sandbox{Engine: "docker", Image: "ubuntu:22.04", Network: "none", WorkspaceRW: true},
		ConfirmFor: "high",
	}
}

type RiskLevel string
const (
	RiskLow RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh RiskLevel = "high"
)

func (p *Policy) Score(code string) (int, []string) {
	sum := 0
	reasons := []string{}
	for _, r := range p.BlacklistPatterns {
		if r.re == nil {
			continue
		}
		if r.re.MatchString(code) {
			sum += r.Score
			if r.Reason != "" {
				reasons = append(reasons, r.Reason)
			}
		}
	}
	return sum, reasons
}

func (p *Policy) Risk(score int) RiskLevel {
	if score >= p.Thresholds.HighMin {
		return RiskHigh
	}
	if score <= p.Thresholds.LowMax {
		return RiskLow
	}
	return RiskMedium
}

func (p *Policy) IsWhitelisted(lang string) bool {
	lang = strings.ToLower(lang)
	for _, w := range p.WhitelistLanguages {
		if w == lang {
			return true
		}
	}
	return false
}
