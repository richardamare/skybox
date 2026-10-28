package sshconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const userConfig = "Host devbox\n  HostName 192.0.2.1\n\nHost *\n  IdentityAgent ~/agent.sock\n"

func TestWithPutsTheAliasFirst(t *testing.T) {
	block := Block("api-fix", "api-fix-sbx.example.com", "/usr/local/bin/skybox", "/home/me/.ssh/skybox_known_hosts")
	got := With(userConfig, "api-fix", block)

	if !strings.HasPrefix(got, "# skybox:begin api-fix\nHost skybox-api-fix\n") || !strings.HasSuffix(got, userConfig) {
		t.Fatalf("With =\n%s", got)
	}
	for _, line := range []string{
		"  HostName api-fix-sbx.example.com",
		"  User agent",
		`  ProxyCommand "/usr/local/bin/skybox" proxy api-fix`,
		"  HostKeyAlias skybox-api-fix",
	} {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("the alias lacks %q", line)
		}
	}
}

func TestWithReplacesAnEarlierBlock(t *testing.T) {
	first := With(userConfig, "api-fix", Block("api-fix", "old", "/skybox", "/kh"))
	got := With(first, "api-fix", Block("api-fix", "new", "/skybox", "/kh"))

	if strings.Count(got, "Host skybox-api-fix") != 1 || strings.Contains(got, "HostName old") {
		t.Fatalf("With =\n%s", got)
	}
}

func TestWithoutRemovesOnlyThatSandbox(t *testing.T) {
	config := With(With(userConfig, "web", Block("web", "web-sbx", "/skybox", "/kh")), "api-fix", Block("api-fix", "api-fix-sbx", "/skybox", "/kh"))
	got := Without(config, "api-fix")

	if strings.Contains(got, "skybox-api-fix") || !strings.Contains(got, "Host skybox-web") || !strings.HasSuffix(got, userConfig) {
		t.Fatalf("Without =\n%s", got)
	}
	if Without(got, "web") != userConfig {
		t.Fatalf("removing every alias did not restore the user's config:\n%s", Without(got, "web"))
	}
}

func TestUpdateCreatesTheFilePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh", "config")
	if err := Update(path, func(c string) string { return With(c, "api-fix", Block("api-fix", "h", "/skybox", "/kh")) }); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v, %v; want mode 0600", info, err)
	}
}

func TestForgetHostDropsOnlyThatAlias(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	content := "skybox-api-fix ssh-ed25519 AAAA\nskybox-web ssh-ed25519 BBBB\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ForgetHost(path, "skybox-api-fix"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "skybox-web ssh-ed25519 BBBB\n" {
		t.Fatalf("known_hosts = %q", got)
	}
	if err := ForgetHost(filepath.Join(t.TempDir(), "missing"), "skybox-api-fix"); err != nil {
		t.Fatalf("ForgetHost(missing) = %v; want nil", err)
	}
}

func TestCatchAllFollowsTheSandboxBlocks(t *testing.T) {
	config := With(userConfig, "api-fix", Block("api-fix", "h", "/skybox", "/kh"))
	got := WithCatchAll(config, CatchAll("/skybox", "/kh"))

	sandbox, catchAll, user := strings.Index(got, "Host skybox-api-fix"), strings.Index(got, "Host skybox-*"), strings.Index(got, "Host devbox")
	if !(sandbox >= 0 && sandbox < catchAll && catchAll < user) {
		t.Fatalf("WithCatchAll =\n%s", got)
	}
	if !strings.Contains(got, `ProxyCommand "/skybox" proxy --alias %n`) {
		t.Fatal("the catch-all does not pass the alias to skybox proxy")
	}
	if again := WithCatchAll(got, CatchAll("/skybox", "/kh")); again != got {
		t.Fatalf("a second WithCatchAll changed the config:\n%s", again)
	}
}

func TestWithoutKeepsTheCatchAll(t *testing.T) {
	config := WithCatchAll(With(userConfig, "api-fix", Block("api-fix", "h", "/skybox", "/kh")), CatchAll("/skybox", "/kh"))
	got := Without(config, "api-fix")

	if strings.Contains(got, "Host skybox-api-fix") || !strings.Contains(got, "Host skybox-*") {
		t.Fatalf("Without =\n%s", got)
	}
}
