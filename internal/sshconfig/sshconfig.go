// Package sshconfig keeps one Host alias per sandbox in ~/.ssh/config, between marker comments that skybox owns.
package sshconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func Alias(sandbox string) string { return "skybox-" + sandbox }

func begin(sandbox string) string { return "# skybox:begin " + sandbox }
func end(sandbox string) string   { return "# skybox:end " + sandbox }

// Block is the alias for one sandbox. ssh reaches it through `skybox proxy`, so the host key it learns belongs to the
// alias rather than to a hostname another sandbox may reuse later.
func Block(sandbox, hostname, skyboxPath, knownHosts string) string {
	return fmt.Sprintf(`%s
Host %s
  HostName %s
  User agent
  ProxyCommand "%s" proxy %s
  HostKeyAlias %s
  UserKnownHostsFile "%s"
  StrictHostKeyChecking accept-new
  ServerAliveInterval 30
%s
`, begin(sandbox), Alias(sandbox), hostname, skyboxPath, sandbox, Alias(sandbox), knownHosts, end(sandbox))
}

// catchAllName keys the block that answers an alias whose sandbox skybox never created.
const catchAllName = "*"

// CatchAll covers every skybox-* alias, so ssh to one with no sandbox behind it reaches skybox proxy, which names the
// missing sandbox, instead of failing on DNS. A sandbox's own block comes first and wins.
func CatchAll(skyboxPath, knownHosts string) string {
	return fmt.Sprintf(`%s
Host %s
  User agent
  ProxyCommand "%s" proxy --alias %%n
  UserKnownHostsFile "%s"
  StrictHostKeyChecking accept-new
%s
`, begin(catchAllName), Alias(catchAllName), skyboxPath, knownHosts, end(catchAllName))
}

// WithCatchAll returns config with the catch-all block after every sandbox block skybox wrote.
func WithCatchAll(config, block string) string {
	rest := Without(config, catchAllName)
	lastEnd := strings.LastIndex(rest, "# skybox:end ")
	if lastEnd < 0 {
		return block + rest
	}
	cut := lastEnd + strings.Index(rest[lastEnd:], "\n") + 1
	return rest[:cut] + block + rest[cut:]
}

// With returns config with the sandbox's block at the top, where no earlier Host * can override its options.
func With(config, sandbox, block string) string {
	return block + Without(config, sandbox)
}

// Without returns config without the sandbox's block, leaving every other line as it was.
func Without(config, sandbox string) string {
	var out []string
	inside := false
	for line := range strings.Lines(config) {
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case trimmed == begin(sandbox):
			inside = true
		case trimmed == end(sandbox):
			inside = false
		case !inside:
			out = append(out, line)
		}
	}
	return strings.Join(out, "")
}

// ForgetHost removes the host keys ssh learned for alias, so a new sandbox under the same name is trusted afresh.
func ForgetHost(knownHosts, alias string) error {
	if _, err := os.Stat(knownHosts); os.IsNotExist(err) {
		return nil
	}
	return Update(knownHosts, func(content string) string {
		var kept []string
		for line := range strings.Lines(content) {
			hosts, _, _ := strings.Cut(line, " ")
			if !slices.Contains(strings.Split(hosts, ","), alias) {
				kept = append(kept, line)
			}
		}
		return strings.Join(kept, "")
	})
}

// Update rewrites the file at path through edit, creating it if missing, and replaces it in one rename.
func Update(path string, edit func(string) string) error {
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	next := edit(string(current))
	if next == string(current) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.skybox-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(next); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
