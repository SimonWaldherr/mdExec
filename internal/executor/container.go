package executor

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/SimonWaldherr/mdexec/internal/policy"
)

func detectEngine(prefer string) string {
	if prefer != "" {
		if _, err := exec.LookPath(prefer); err == nil {
			return prefer
		}
	}
	for _, e := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(e); err == nil {
			return e
		}
	}
	return ""
}

// Build container run command wrapper if engine/image present. Returns nil if not applicable.
func wrapContainer(engine string, image string, cwd string, baseCmd []string, useStdin bool, sandbox policy.Sandbox, passEnv map[string]string) []string {
	if engine == "" || image == "" {
		return nil
	}
	args := []string{engine, "run", "--rm"}
	if useStdin {
		args = append(args, "-i")
	}
	abs, _ := filepath.Abs(cwd)
	rwFlag := ":ro"
	if sandbox.WorkspaceRW {
		rwFlag = ":rw"
	}
	args = append(args, "-v", fmt.Sprintf("%s:/workspace%s", abs, rwFlag), "-w", "/workspace")
	switch sandbox.Network {
	case "none":
		args = append(args, "--network", "none")
	case "restricted":
		// leave as default or apply user-defined net ns (skipped here)
	case "full":
		// nothing
	}
	for k, v := range passEnv {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	args = append(args, image)
	args = append(args, baseCmd...)
	return args
}
