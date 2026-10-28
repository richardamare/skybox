// Package sshkeys reads the public keys the user's ssh offers, so a sandbox trusts the keys the Mac already has and
// skybox never creates one.
package sshkeys

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// FromAgent lists the public keys in the agent that ssh uses for host, which may be one that ssh_config names with
// IdentityAgent rather than $SSH_AUTH_SOCK.
func FromAgent(ctx context.Context, host string) ([]string, error) {
	config, err := exec.CommandContext(ctx, "ssh", "-G", host).Output()
	if err != nil {
		return nil, fmt.Errorf("read the ssh configuration for %s: %w", host, err)
	}
	socket := identityAgent(string(config))
	if socket == "" {
		socket = os.Getenv("SSH_AUTH_SOCK")
	}
	if socket == "" {
		return nil, errors.New("no SSH agent is configured: set IdentityAgent in ~/.ssh/config or SSH_AUTH_SOCK")
	}

	cmd := exec.CommandContext(ctx, "ssh-add", "-L")
	cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+socket)
	out, err := cmd.Output()
	keys := PublicKeys(string(out))
	if len(keys) == 0 {
		return nil, fmt.Errorf("the SSH agent at %s holds no keys (%v): add one, so a sandbox has a key to trust", socket, err)
	}
	return keys, nil
}

func identityAgent(sshG string) string {
	scanner := bufio.NewScanner(strings.NewReader(sshG))
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "identityagent "); ok && value != "none" {
			return value
		}
	}
	return ""
}

// PublicKeys keeps the lines of `ssh-add -L` output that are keys, which drops its "no identities" message.
func PublicKeys(sshAddL string) []string {
	var keys []string
	for line := range strings.Lines(sshAddL) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ssh-") || strings.HasPrefix(line, "ecdsa-") || strings.HasPrefix(line, "sk-") {
			keys = append(keys, line)
		}
	}
	return keys
}
