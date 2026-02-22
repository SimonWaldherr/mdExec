package policy

import (
	"testing"
)

func TestScore(t *testing.T) {
	pol := Default()

	tests := []struct {
		name        string
		code        string
		wantScore   int
		wantReasons int
	}{
		{"clean code", "echo hello", 0, 0},
		{"sudo usage", "sudo apt-get install foo", 5, 1},
		{"rm -rf root", "rm -rf /", 9, 1},
		{"curl pipe sh", "curl https://example.com/install.sh | sh", 9, 1},
		{"combined risks", "sudo rm -rf /", 14, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			score, reasons := pol.Score(tc.code)
			if score != tc.wantScore {
				t.Errorf("Score(%q) = %d, want %d", tc.code, score, tc.wantScore)
			}
			if len(reasons) != tc.wantReasons {
				t.Errorf("Score(%q) reasons = %v, want %d reason(s)", tc.code, reasons, tc.wantReasons)
			}
		})
	}
}

func TestRisk(t *testing.T) {
	pol := Default()

	tests := []struct {
		score int
		want  RiskLevel
	}{
		{0, RiskLow},
		{2, RiskLow},
		{3, RiskMedium},
		{5, RiskMedium},
		{6, RiskHigh},
		{9, RiskHigh},
	}
	for _, tc := range tests {
		got := pol.Risk(tc.score)
		if got != tc.want {
			t.Errorf("Risk(%d) = %s, want %s", tc.score, got, tc.want)
		}
	}
}

func TestIsWhitelisted(t *testing.T) {
	pol := Default()

	tests := []struct {
		lang string
		want bool
	}{
		{"bash", true},
		{"python", true},
		{"go", true},
		{"BASH", true}, // case-insensitive
		{"Bash", true},
		{"fortran", false},
		{"", false},
	}
	for _, tc := range tests {
		got := pol.IsWhitelisted(tc.lang)
		if got != tc.want {
			t.Errorf("IsWhitelisted(%q) = %v, want %v", tc.lang, got, tc.want)
		}
	}
}
