package toolkit

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestThePluginsAreAgentConfigMattpocockAndPstack(t *testing.T) {
	want := []Plugin{
		{ID: "agent-config@agent-config", Marketplace: "agent-config"},
		{ID: "mattpocock-skills@mattpocock", Marketplace: "mattpocock", Repo: "mattpocock/skills"},
		{ID: "pstack@pstack-claude", Marketplace: "pstack-claude", Repo: "michael-denyer/pstack-claude"},
	}
	if len(Plugins) != len(want) {
		t.Fatalf("Plugins = %+v; want %+v", Plugins, want)
	}
	for i := range want {
		if Plugins[i] != want[i] {
			t.Fatalf("Plugins[%d] = %+v; want %+v", i, Plugins[i], want[i])
		}
	}
}

func TestArchiveExtractsToTheSameTreeWithModesSymlinksAndGit(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		".git/HEAD":              "ref: refs/heads/master\n",
		"hooks/format.sh":        "#!/bin/sh\n",
		"instructions/AGENTS.md": "# Rules\n",
	} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(dir, "hooks/format.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("instructions/AGENTS.md", filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := Archive(&archive, dir); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	extract := exec.Command("tar", "-x", "-m", "-f", "-", "-C", out)
	extract.Stdin = &archive
	if msg, err := extract.CombinedOutput(); err != nil {
		t.Fatalf("tar -x: %v\n%s", err, msg)
	}
	if head, err := os.ReadFile(filepath.Join(out, ".git/HEAD")); err != nil || string(head) != "ref: refs/heads/master\n" {
		t.Fatalf(".git/HEAD = %q, %v; want the clone's", head, err)
	}
	if info, err := os.Stat(filepath.Join(out, "hooks/format.sh")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("hooks/format.sh = %v, %v; want it executable", info, err)
	}
	if link, err := os.Readlink(filepath.Join(out, "CLAUDE.md")); err != nil || link != "instructions/AGENTS.md" {
		t.Fatalf("CLAUDE.md links to %q, %v; want instructions/AGENTS.md", link, err)
	}
}

func TestArchiveFollowsAClonePathThatIsASymlink(t *testing.T) {
	clone := t.TempDir()
	if err := os.WriteFile(filepath.Join(clone, "README.md"), []byte("agent-config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "agent-config")
	if err := os.Symlink(clone, link); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := Archive(&archive, link); err != nil {
		t.Fatal(err)
	}

	header, err := tar.NewReader(&archive).Next()
	if err != nil || header.Name != "README.md" {
		t.Fatalf("first entry = %+v, %v; want the clone's README.md", header, err)
	}
}

func TestCheckCloneNamesAMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent-config")
	err := CheckClone(dir)

	var missing *MissingCloneError
	if !errors.As(err, &missing) || err.Error() != "no agent-config clone at "+dir {
		t.Fatalf("CheckClone = %v; want a MissingCloneError naming %s", err, dir)
	}
}

func TestCheckCloneNeedsTheInstructions(t *testing.T) {
	dir := t.TempDir()
	err := CheckClone(dir)

	var missing *MissingCloneError
	if err == nil || errors.As(err, &missing) || !strings.Contains(err.Error(), Instructions) {
		t.Fatalf("CheckClone = %v; want an error naming %s", err, Instructions)
	}
	if err := os.MkdirAll(filepath.Join(dir, "instructions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, Instructions), []byte("# Rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckClone(dir); err != nil {
		t.Fatalf("CheckClone with the instructions = %v", err)
	}
}

func TestCheckInstalledReturnsThePluginsInTheirOrder(t *testing.T) {
	got, err := CheckInstalled([]byte(`[
		{"id":"pstack@pstack-claude","version":"0.9.111","scope":"user"},
		{"id":"agent-config@agent-config","version":"666a5ea73a02","scope":"user"},
		{"id":"mattpocock-skills@mattpocock","version":"1.3.1","scope":"user"}
	]`))

	want := []Installed{
		{"agent-config@agent-config", "666a5ea73a02"},
		{"mattpocock-skills@mattpocock", "1.3.1"},
		{"pstack@pstack-claude", "0.9.111"},
	}
	if err != nil || len(got) != len(want) {
		t.Fatalf("CheckInstalled = %+v, %v; want %+v", got, err, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CheckInstalled[%d] = %+v; want %+v", i, got[i], want[i])
		}
	}
}

func TestCheckInstalledFailsOnAnyOtherList(t *testing.T) {
	for name, list := range map[string]string{
		"one missing": `[{"id":"agent-config@agent-config"},{"id":"pstack@pstack-claude"}]`,
		"one more": `[{"id":"agent-config@agent-config"},{"id":"mattpocock-skills@mattpocock"},{"id":"pstack@pstack-claude"},
			{"id":"frontend-design@claude-plugins-official","scope":"project"}]`,
		"not JSON": `Installed plugins:`,
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := CheckInstalled([]byte(list)); err == nil {
				t.Fatalf("CheckInstalled = %+v; want an error", got)
			}
		})
	}
}

func TestQuoteIsOneShellWord(t *testing.T) {
	for _, s := range []string{"plain", "it's", `"$HOME" $(id) \n`, ""} {
		out, err := exec.Command("sh", "-c", "printf %s "+Quote(s)).Output()
		if err != nil || string(out) != s {
			t.Fatalf("sh printed %q, %v for Quote(%q); want it unchanged", out, err, s)
		}
	}
}

func TestScriptNamesEveryPluginAndItsMarketplace(t *testing.T) {
	script := Script()
	for _, p := range Plugins {
		for _, line := range []string{
			"claude plugin marketplace update " + Quote(p.Marketplace),
			"claude plugin install " + Quote(p.ID),
			"claude plugin update " + Quote(p.ID),
		} {
			if !strings.Contains(script, line+"\n") {
				t.Fatalf("the script lacks %q:\n%s", line, script)
			}
		}
		if p.Repo != "" && !strings.Contains(script, "claude plugin marketplace add "+Quote(p.Repo)+"\n") {
			t.Fatalf("the script does not add %s from GitHub:\n%s", p.Repo, script)
		}
	}
	if out, err := exec.Command("bash", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}
}
