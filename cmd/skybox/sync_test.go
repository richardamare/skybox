package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/richardamare/skybox/internal/sandbox"
	"github.com/richardamare/skybox/internal/toolkit"
)

var toolkitPlugins = []string{"agent-config@agent-config", "mattpocock-skills@mattpocock", "pstack@pstack-claude"}

func TestSyncGivesTheSandboxTheThreePluginsAndNoOther(t *testing.T) {
	d := testDeps(t, newFakeProvider(runningSandbox("web")))
	sandboxes := withToolkit(t, &d)
	writeFiles(t, sandboxes.home("skybox-web"), map[string]string{".fake-claude/installed": "frontend-design@claude-plugins-official\n"})
	got := runCLI(t, d, "sync", "web")

	installed := sandboxes.installed("skybox-web")
	slices.Sort(installed)
	if got.code != 0 || !slices.Equal(installed, toolkitPlugins) {
		t.Fatalf("sync web = %d, stderr %q, installed %v; want exactly %v", got.code, got.stderr, installed, toolkitPlugins)
	}
	for _, id := range toolkitPlugins {
		if !strings.Contains(got.stdout, id) {
			t.Fatalf("sync web printed %q; want it to name %s", got.stdout, id)
		}
	}
}

func TestSyncMakesCLAUDEmdAgentConfigsInstructionsByteForByte(t *testing.T) {
	d := testDeps(t, newFakeProvider(runningSandbox("web")))
	sandboxes := withToolkit(t, &d)
	runCLI(t, d, "sync", "web")

	want, _ := os.ReadFile(filepath.Join(d.agentConfig, toolkit.Instructions))
	got, err := os.ReadFile(filepath.Join(sandboxes.home("skybox-web"), ".claude", "CLAUDE.md"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("CLAUDE.md = %q, %v; want %q", got, err, want)
	}
}

func TestSyncInstallsAgentConfigFromACopyOfTheMacsCloneAndTheOthersFromGitHub(t *testing.T) {
	d := testDeps(t, newFakeProvider(runningSandbox("web")))
	sandboxes := withToolkit(t, &d)
	runCLI(t, d, "sync", "web")

	copyDir := filepath.Join(sandboxes.home("skybox-web"), ".skybox", "marketplaces", "agent-config")
	assertSameTree(t, d.agentConfig, copyDir)
	calls := sandboxes.claudeCalls("skybox-web")
	for _, want := range []string{
		"plugin marketplace add " + copyDir,
		"plugin marketplace add mattpocock/skills",
		"plugin marketplace add michael-denyer/pstack-claude",
	} {
		if !slices.Contains(calls, want) {
			t.Fatalf("claude ran %q; want %q", calls, want)
		}
	}
}

func TestSyncBringsTheSandboxToTheMacsAgentConfig(t *testing.T) {
	d := testDeps(t, newFakeProvider(runningSandbox("web")))
	sandboxes := withToolkit(t, &d)
	runCLI(t, d, "sync", "web")

	writeFiles(t, d.agentConfig, map[string]string{toolkit.Instructions: "# Rules, revised\n"})
	if err := os.Remove(filepath.Join(d.agentConfig, "plugin/skills/review/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	version := commitAll(t, d.agentConfig, "second")
	got := runCLI(t, d, "sync", "web", "--json")

	var doc syncResult
	decodeOne(t, got.stdout, &doc)
	if got.code != 0 || doc.Plugins[0] != (toolkit.Installed{ID: "agent-config@agent-config", Version: version}) {
		t.Fatalf("sync web --json = %d, %+v, stderr %q; want agent-config at %s", got.code, doc, got.stderr, version)
	}
	claudeMD, _ := os.ReadFile(filepath.Join(sandboxes.home("skybox-web"), ".claude", "CLAUDE.md"))
	if string(claudeMD) != "# Rules, revised\n" {
		t.Fatalf("CLAUDE.md = %q; want the revised rules", claudeMD)
	}
	assertSameTree(t, d.agentConfig, filepath.Join(sandboxes.home("skybox-web"), ".skybox", "marketplaces", "agent-config"))
}

func TestSyncJSONReportsEachPluginWithItsVersion(t *testing.T) {
	d := testDeps(t, newFakeProvider(runningSandbox("web")))
	withToolkit(t, &d)
	got := runCLI(t, d, "sync", "web", "--json")

	var doc syncResult
	decodeOne(t, got.stdout, &doc)
	var ids []string
	for _, p := range doc.Plugins {
		ids = append(ids, p.ID)
		if p.Version == "" {
			t.Fatalf("plugin %s has no version in %+v", p.ID, doc)
		}
	}
	if got.code != 0 || doc.Name != "web" || !slices.Equal(ids, toolkitPlugins) || doc.Instructions != "~/.claude/CLAUDE.md" {
		t.Fatalf("sync web --json = %d, %+v", got.code, doc)
	}
}

func TestSyncFailsLikeProxyForASleepingOrMissingSandbox(t *testing.T) {
	asleep := runningSandbox("web")
	asleep.State = sandbox.Asleep
	for _, tc := range []struct {
		name     string
		existing []sandbox.Sandbox
	}{
		{"asleep", []sandbox.Sandbox{asleep}},
		{"missing", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newFakeProvider(tc.existing...)
			d := testDeps(t, p)
			sandboxes := withToolkit(t, &d)
			proxy := runCLI(t, d, "proxy", "web")
			got := runCLI(t, d, "sync", "web")

			if got.code != 1 || got.stderr != proxy.stderr {
				t.Fatalf("sync web = %d, stderr %q; want exit 1 and proxy's %q", got.code, got.stderr, proxy.stderr)
			}
			if len(sandboxes.calls) != 0 || len(p.created) != 0 || len(p.sandboxes) != len(tc.existing) {
				t.Fatalf("sync changed something: ssh to %v, created %v, sandboxes %v", sandboxes.calls, p.created, p.sandboxes)
			}
			if s, ok := p.sandboxes["web"]; ok && s.State != sandbox.Asleep {
				t.Fatalf("sync woke web: %s", s.State)
			}
		})
	}
}

func TestSyncWithoutAnAgentConfigCloneFailsNamingTheDirectory(t *testing.T) {
	d := testDeps(t, newFakeProvider(runningSandbox("web")))
	got := runCLI(t, d, "sync", "web")

	if got.code != 1 || !strings.Contains(got.stderr, "no agent-config clone at "+d.agentConfig) {
		t.Fatalf("sync web = %d, stderr %q; want exit 1 naming %s", got.code, got.stderr, d.agentConfig)
	}
}

func TestSyncFailsWhenTheCloneHasNoInstructions(t *testing.T) {
	d := testDeps(t, newFakeProvider(runningSandbox("web")))
	sandboxes := withToolkit(t, &d)
	if err := os.Remove(filepath.Join(d.agentConfig, toolkit.Instructions)); err != nil {
		t.Fatal(err)
	}
	got := runCLI(t, d, "sync", "web")

	if got.code != 1 || !strings.Contains(got.stderr, toolkit.Instructions) || len(sandboxes.calls) != 0 {
		t.Fatalf("sync web = %d, stderr %q, ssh to %v; want exit 1 naming %s before ssh", got.code, got.stderr, sandboxes.calls, toolkit.Instructions)
	}
}

func TestSyncFailsWhenSSHFails(t *testing.T) {
	d := testDeps(t, newFakeProvider(runningSandbox("web")))
	withToolkit(t, &d)
	d.ssh = func(context.Context, string, string, io.Reader, io.Writer, io.Writer) error {
		return errors.New("exit status 255")
	}
	got := runCLI(t, d, "sync", "web")

	if got.code != 1 || !strings.Contains(got.stderr, "sync the agent toolkit to web: exit status 255") {
		t.Fatalf("sync web = %d, stderr %q; want exit 1 naming the failed sync", got.code, got.stderr)
	}
}

// assertSameTree fails unless dir holds the same paths as want, with the same contents, modes and symlinks.
func assertSameTree(t *testing.T, want, dir string) {
	t.Helper()
	read := func(root string) map[string]string {
		tree := map[string]string{}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || path == root {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			info, err := entry.Info()
			if err != nil {
				return err
			}
			switch {
			case info.Mode()&fs.ModeSymlink != 0:
				link, err := os.Readlink(path)
				tree[rel] = "-> " + link
				return err
			case info.IsDir():
				tree[rel] = "dir"
			default:
				data, err := os.ReadFile(path)
				tree[rel] = info.Mode().Perm().String() + " " + string(data)
				return err
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return tree
	}
	wantTree, gotTree := read(want), read(dir)
	if len(gotTree) != len(wantTree) {
		t.Fatalf("%s holds %d paths; want the %d of %s", dir, len(gotTree), len(wantTree), want)
	}
	for path, entry := range wantTree {
		if gotTree[path] != entry {
			t.Fatalf("%s in the copy = %q; want %q", path, gotTree[path], entry)
		}
	}
}

func TestUpSyncsTheToolkitLastOverTheNewAlias(t *testing.T) {
	d := testDeps(t, newFakeProvider())
	sandboxes := withToolkit(t, &d)
	ts := tailnetOf(t, d)
	sandboxes.beforeRun = func(alias string) {
		config, _ := os.ReadFile(d.sshConfig)
		if !strings.Contains(string(config), "Host "+alias+"\n") || len(ts.devices) != 1 {
			t.Fatalf("up synced before the alias and the tailnet device existed:\n%s", config)
		}
	}
	got := runCLI(t, d, "up", "web", "--json")

	var doc sandboxResult
	decodeOne(t, got.stdout, &doc)
	installed := sandboxes.installed("skybox-web")
	slices.Sort(installed)
	if got.code != 0 || doc.Name != "web" || !slices.Equal(installed, toolkitPlugins) {
		t.Fatalf("up web = %d, %+v, stderr %q, installed %v; want web with %v", got.code, doc, got.stderr, installed, toolkitPlugins)
	}
	if _, err := os.Stat(filepath.Join(sandboxes.home("skybox-web"), ".claude", "CLAUDE.md")); err != nil {
		t.Fatalf("up left web without CLAUDE.md: %v", err)
	}
}

func TestWakeSyncsTheToolkitBeforeItReturns(t *testing.T) {
	asleep := runningSandbox("web")
	asleep.State = sandbox.Asleep
	p := newFakeProvider(asleep)
	d := testDeps(t, p)
	tunnelsOf(t, d).tunnels["tunnel-skybox-web"] = true
	sandboxes := withToolkit(t, &d)
	sandboxes.beforeRun = func(string) {
		if p.sandboxes["web"].State != sandbox.Running {
			t.Fatalf("wake synced a sandbox that is %s", p.sandboxes["web"].State)
		}
	}
	first := runCLI(t, d, "wake", "web")

	installed := sandboxes.installed("skybox-web")
	slices.Sort(installed)
	if first.code != 0 || !slices.Equal(installed, toolkitPlugins) {
		t.Fatalf("wake web = %d, stderr %q, installed %v; want %v", first.code, first.stderr, installed, toolkitPlugins)
	}

	writeFiles(t, d.agentConfig, map[string]string{toolkit.Instructions: "# Rules, revised\n"})
	version := commitAll(t, d.agentConfig, "second")
	p.sandboxes["web"] = asleep
	second := runCLI(t, d, "wake", "web")

	home := sandboxes.home("skybox-web")
	claudeMD, _ := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	copied := git(t, filepath.Join(home, ".skybox", "marketplaces", "agent-config"), "rev-parse", "--short=12", "HEAD")
	if second.code != 0 || string(claudeMD) != "# Rules, revised\n" || copied != version {
		t.Fatalf("wake web = %d, stderr %q, CLAUDE.md %q, agent-config at %s; want the revised rules at %s", second.code, second.stderr, claudeMD, copied, version)
	}
	if calls := sandboxes.claudeCalls("skybox-web"); !slices.Contains(calls, "plugin update agent-config@agent-config") {
		t.Fatalf("claude ran %q; want agent-config's version updated", calls)
	}
}

func TestUpAndWakeSkipTheToolkitWithoutACloneAndSaySo(t *testing.T) {
	asleep := runningSandbox("web")
	asleep.State = sandbox.Asleep
	for _, args := range [][]string{{"up", "api-fix"}, {"wake", "web"}} {
		t.Run(args[0], func(t *testing.T) {
			d := testDeps(t, newFakeProvider(asleep))
			tunnelsOf(t, d).tunnels["tunnel-skybox-web"] = true
			got := runCLI(t, d, args...)

			want := "skipped the agent toolkit on " + args[1] + ": no agent-config clone at " + d.agentConfig + ", so run skybox sync " + args[1] + " once it is there\n"
			if got.code != 0 || !strings.Contains(got.stderr, want) {
				t.Fatalf("%v = %d, stderr %q; want exit 0 and %q", args, got.code, got.stderr, want)
			}
		})
	}
}

func TestUpAndWakeFailWhenTheToolkitDoesNotSyncAndKeepTheSandbox(t *testing.T) {
	asleep := runningSandbox("web")
	asleep.State = sandbox.Asleep
	for _, args := range [][]string{{"up", "api-fix"}, {"wake", "web"}} {
		t.Run(args[0], func(t *testing.T) {
			p := newFakeProvider(asleep)
			d := testDeps(t, p)
			tunnelsOf(t, d).tunnels["tunnel-skybox-web"] = true
			withToolkit(t, &d)
			d.ssh = func(context.Context, string, string, io.Reader, io.Writer, io.Writer) error {
				return errors.New("exit status 255")
			}
			got := runCLI(t, d, args...)

			want := args[1] + " is running, but its agent toolkit did not sync, so run skybox sync " + args[1] + " to try again"
			if got.code != 1 || !strings.Contains(got.stderr, want) || p.sandboxes[args[1]].State != sandbox.Running {
				t.Fatalf("%v = %d, stderr %q, sandboxes %v; want exit 1, %q and the sandbox running", args, got.code, got.stderr, p.sandboxes, want)
			}
		})
	}
}
