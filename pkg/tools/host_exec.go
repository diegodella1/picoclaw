package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// HostExecTool executes commands on the host system via nsenter.
// Requires the container to run with --privileged --pid=host.
type HostExecTool struct {
	timeout      time.Duration
	denyPatterns []*regexp.Regexp
}

func NewHostExecTool() *HostExecTool {
	denyPatterns := []*regexp.Regexp{
		regexp.MustCompile(`\brm\s+-[rf]{1,2}\s+/\s*$`), // only block rm -rf /
		regexp.MustCompile(`\b(mkfs|diskpart)\b\s`),
		regexp.MustCompile(`\bdd\s+if=.*of=/dev/sd`),
		regexp.MustCompile(`>\s*/dev/sd[a-z]\b`),
		regexp.MustCompile(`:\(\)\s*\{.*\};\s*:`), // fork bomb
	}

	return &HostExecTool{
		timeout:      60 * time.Second,
		denyPatterns: denyPatterns,
	}
}

func (t *HostExecTool) Name() string {
	return "host_exec"
}

func (t *HostExecTool) Description() string {
	return "Execute a command directly on the Raspberry Pi host (not inside the container). " +
		"Use this for system administration: systemctl, docker, apt, networking, file management, etc. " +
		"Commands run as root on the host via nsenter."
}

func (t *HostExecTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "The shell command to execute on the host",
			},
			"working_dir": map[string]interface{}{
				"type":        "string",
				"description": "Optional working directory on the host",
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Optional timeout in seconds (default 60, max 300)",
			},
		},
		"required": []string{"command"},
	}
}

func (t *HostExecTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	command, ok := args["command"].(string)
	if !ok {
		return ErrorResult("command is required")
	}

	if guardError := t.guardCommand(command); guardError != "" {
		return ErrorResult(guardError)
	}

	// Build the actual command to run
	shellCmd := command
	if wd, ok := args["working_dir"].(string); ok && wd != "" {
		shellCmd = fmt.Sprintf("cd %q && %s", wd, command)
	}

	timeout := t.timeout
	if secs, ok := args["timeout"].(float64); ok && secs > 0 {
		custom := time.Duration(secs) * time.Second
		if custom > 300*time.Second {
			custom = 300 * time.Second
		}
		timeout = custom
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// nsenter into PID 1's namespaces = run on the host
	cmd := exec.CommandContext(cmdCtx, "nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--", "sh", "-c", shellCmd)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	if err != nil && cmdCtx.Err() == context.DeadlineExceeded {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		msg := fmt.Sprintf("Command timed out after %v and was killed. Use the 'timeout' parameter for longer commands.", timeout)
		return &ToolResult{ForLLM: msg, ForUser: msg, IsError: true}
	}

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nSTDERR:\n" + stderr.String()
	}
	if err != nil {
		output += fmt.Sprintf("\nExit code: %v", err)
	}
	if output == "" {
		output = "(no output)"
	}

	maxLen := 10000
	if len(output) > maxLen {
		output = output[:maxLen] + fmt.Sprintf("\n... (truncated, %d more chars)", len(output)-maxLen)
	}

	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
		IsError: err != nil,
	}
}

func (t *HostExecTool) guardCommand(command string) string {
	lower := strings.ToLower(strings.TrimSpace(command))

	for _, pattern := range t.denyPatterns {
		if pattern.MatchString(lower) {
			return "Command blocked by safety guard (dangerous pattern detected)"
		}
	}

	return ""
}
