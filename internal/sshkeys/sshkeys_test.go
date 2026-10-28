package sshkeys

import (
	"reflect"
	"testing"
)

func TestPublicKeysDropsTheNoIdentitiesMessage(t *testing.T) {
	if keys := PublicKeys("The agent has no identities.\n"); len(keys) != 0 {
		t.Fatalf("PublicKeys = %v; want none", keys)
	}
}

func TestPublicKeysKeepsEveryKeyLine(t *testing.T) {
	got := PublicKeys("ssh-ed25519 AAAA GitHub\nssh-rsa BBBB Azure\necdsa-sha2-nistp256 CCCC\n")
	want := []string{"ssh-ed25519 AAAA GitHub", "ssh-rsa BBBB Azure", "ecdsa-sha2-nistp256 CCCC"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PublicKeys = %v; want %v", got, want)
	}
}

func TestIdentityAgentReadsTheConfiguredSocket(t *testing.T) {
	sshG := "user me\nidentityagent /Users/me/Library/Group Containers/agent.sock\nidentityfile ~/.ssh/id_rsa\n"
	if got := identityAgent(sshG); got != "/Users/me/Library/Group Containers/agent.sock" {
		t.Fatalf("identityAgent = %q", got)
	}
	if got := identityAgent("identityagent none\n"); got != "" {
		t.Fatalf("identityAgent(none) = %q; want empty", got)
	}
}
