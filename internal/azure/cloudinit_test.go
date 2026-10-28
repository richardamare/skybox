package azure

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type cloudConfig struct {
	Users      []any `yaml:"users"`
	WriteFiles []struct {
		Path        string `yaml:"path"`
		Content     string `yaml:"content"`
		Permissions string `yaml:"permissions"`
	} `yaml:"write_files"`
	Runcmd []any `yaml:"runcmd"`
}

func render(t *testing.T) (string, cloudConfig) {
	t.Helper()
	out, err := renderCloudInit(cloudInitData{
		Name:             "api-fix",
		SSHKeys:          []string{"ssh-ed25519 AAAA laptop", `ssh-rsa BBBB "quoted": key`},
		TunnelToken:      "tunnel-token",
		TailnetAuthKey:   "tskey-auth-k1-abc",
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
	script := file(t, config, "/usr/local/sbin/skybox-t3-env")

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

func TestCloudInitPublishesT3CodeWithTailscaleServe(t *testing.T) {
	_, config := render(t)
	script := file(t, config, "/usr/local/sbin/skybox-t3-env")

	if !strings.Contains(script, `T3CODE_HOST=127.0.0.1\n`) || !strings.Contains(script, `T3CODE_TAILSCALE_SERVE=true\n`) {
		t.Fatalf("the T3 Code service environment does not keep the server on loopback behind Tailscale Serve:\n%s", script)
	}
}

func TestOnlyRootReadsTheKeysTheSandboxBootsWith(t *testing.T) {
	_, config := render(t)
	for path, want := range map[string]string{
		"/etc/cloudflared/token":           "tunnel-token",
		"/var/lib/skybox/tailnet-auth-key": "tskey-auth-k1-abc",
	} {
		found := false
		for _, f := range config.WriteFiles {
			if f.Path == path {
				found = true
				if f.Content != want || f.Permissions != "0600" {
					t.Errorf("%s = %q with mode %s; want %q, mode 0600", path, f.Content, f.Permissions, want)
				}
			}
		}
		if !found {
			t.Errorf("cloud-init writes no %s", path)
		}
	}
}

func TestCloudInitJoinsTheTailnetAsTheSandboxAndRemovesTheAuthKey(t *testing.T) {
	_, config := render(t)
	script := file(t, config, "/usr/local/sbin/skybox-join-tailnet")

	for _, want := range []string{
		"tailscale up --auth-key=file:/var/lib/skybox/tailnet-auth-key --hostname=api-fix --operator=agent",
		"trap 'rm -f /var/lib/skybox/tailnet-auth-key' EXIT",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the join script lacks %q:\n%s", want, script)
		}
	}
}

func TestCloudInitJoinsTheTailnetBeforeTheT3ServerFirstStarts(t *testing.T) {
	_, config := render(t)
	install, join, setup := -1, -1, -1
	for i, command := range config.Runcmd {
		line := fmt.Sprint(command)
		switch {
		case strings.Contains(line, "apt-get install") && strings.Contains(line, "tailscale"):
			install = i
		case strings.Contains(line, "/usr/local/sbin/skybox-join-tailnet"):
			join = i
		case strings.Contains(line, "agent-setup.sh"):
			setup = i
		}
	}
	if install < 0 || join <= install || setup <= join {
		t.Fatalf("runcmd installs tailscale at %d, joins at %d and starts T3 Code at %d; want them in that order:\n%v",
			install, join, setup, config.Runcmd)
	}
}

func TestCloudflaredStartsLastAndReadsTheTokenFile(t *testing.T) {
	_, config := render(t)
	unit := file(t, config, "/etc/systemd/system/cloudflared.service")
	if !strings.Contains(unit, "--token-file /etc/cloudflared/token") {
		t.Fatalf("cloudflared does not read the token file:\n%s", unit)
	}
	last, _ := config.Runcmd[len(config.Runcmd)-1].([]any)
	if len(last) != 4 || last[3] != "cloudflared.service" {
		t.Fatalf("the last command is %v; want cloudflared to start last", last)
	}
}

func TestCloudInitNeverLinksTheSandboxToT3Connect(t *testing.T) {
	out, _ := render(t)
	for _, command := range []string{"t3 connect", "t3 cloud", "t3 relay", "t3 login"} {
		if strings.Contains(out, command) {
			t.Errorf("cloud-init runs %q", command)
		}
	}
}
