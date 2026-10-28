package azure

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type cloudConfig struct {
	Users      []any `yaml:"users"`
	WriteFiles []struct {
		Path    string `yaml:"path"`
		Content string `yaml:"content"`
	} `yaml:"write_files"`
	Runcmd []any `yaml:"runcmd"`
}

func render(t *testing.T) (string, cloudConfig) {
	t.Helper()
	out, err := renderCloudInit(cloudInitData{
		SSHKeys:          []string{"ssh-ed25519 AAAA laptop", `ssh-rsa BBBB "quoted": key`},
		KeyVaultName:     "kv-skybox-prod-abc123",
		IdentityClientID: "client-id",
		T3Version:        "1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "#cloud-config\n") {
		t.Fatal("cloud-init needs the #cloud-config header")
	}
	var config cloudConfig
	if err := yaml.Unmarshal(out, &config); err != nil {
		t.Fatalf("cloud-init is not valid YAML: %v\n%s", err, out)
	}
	return string(out), config
}

func file(t *testing.T, config cloudConfig, path string) string {
	t.Helper()
	for _, f := range config.WriteFiles {
		if f.Path == path {
			return f.Content
		}
	}
	t.Fatalf("cloud-init writes no %s", path)
	return ""
}

func agentUser(t *testing.T, config cloudConfig) map[string]any {
	t.Helper()
	for _, u := range config.Users {
		if m, ok := u.(map[string]any); ok && m["name"] == "agent" {
			return m
		}
	}
	t.Fatal("cloud-init creates no agent account")
	return nil
}

func TestAgentAccountHasNoSudo(t *testing.T) {
	_, config := render(t)
	agent := agentUser(t, config)

	for _, key := range []string{"sudo", "groups"} {
		if _, ok := agent[key]; ok {
			t.Errorf("the agent account sets %q", key)
		}
	}
}

func TestAgentAccountTrustsEveryKey(t *testing.T) {
	_, config := render(t)
	keys, _ := agentUser(t, config)["ssh_authorized_keys"].([]any)

	if len(keys) != 2 || keys[1] != `ssh-rsa BBBB "quoted": key` {
		t.Fatalf("agent keys = %v", keys)
	}
}

func TestCloudInitTurnsOnLinkedEnvironments(t *testing.T) {
	_, config := render(t)
	setup := file(t, config, "/usr/local/lib/skybox/agent-setup.sh")

	if !strings.Contains(setup, `{"linkedEnvironmentsEnabled":true}`) || !strings.Contains(setup, ".t3/userdata/settings.json") {
		t.Fatalf("agent setup does not turn on linked environments:\n%s", setup)
	}
}

func TestCloudInitRunsThePinnedT3ServerAsAUserService(t *testing.T) {
	_, config := render(t)
	setup := file(t, config, "/usr/local/lib/skybox/agent-setup.sh")

	if !strings.Contains(setup, "T3CODE_VERSION=1.2.3") || !strings.Contains(setup, "t3 service install") {
		t.Fatalf("agent setup does not install the pinned T3 Code service:\n%s", setup)
	}
}

func TestCloudInitReadsTheClaudeTokenThroughTheIdentity(t *testing.T) {
	out, config := render(t)
	script := file(t, config, "/usr/local/sbin/skybox-claude-token")

	if !strings.Contains(script, "client_id=client-id") || !strings.Contains(script, "https://kv-skybox-prod-abc123.vault.azure.net/secrets/claude-code-oauth-token") {
		t.Fatalf("token script does not read Key Vault through the sandbox identity:\n%s", script)
	}
	if !strings.Contains(script, "-m 0700 /home/agent/.t3") || !strings.Contains(script, "umask 077") {
		t.Fatal("the token file is readable beyond the agent account")
	}
	if strings.Contains(out, "sk-ant-") {
		t.Fatal("cloud-init carries a Claude token")
	}
}
