// Package toolkit gives a sandbox the agent toolkit: three Claude Code plugins and the global CLAUDE.md from the Mac's
// agent-config clone. See ADR 0007.
package toolkit

import (
	"archive/tar"
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
)

// Plugin is a Claude Code plugin every sandbox gets, with the marketplace it installs from.
type Plugin struct {
	// ID is the plugin as Claude Code names it, plugin@marketplace.
	ID          string
	Marketplace string
	// Repo is the GitHub repository the sandbox adds the marketplace from. It is empty for agent-config, which
	// reaches the sandbox as a copy of the Mac's clone.
	Repo string
}

// Plugins is the whole toolkit's plugin list. A sandbox has these plugins and no other.
var Plugins = []Plugin{
	{ID: "agent-config@agent-config", Marketplace: "agent-config"},
	{ID: "mattpocock-skills@mattpocock", Marketplace: "mattpocock", Repo: "mattpocock/skills"},
	{ID: "pstack@pstack-claude", Marketplace: "pstack-claude", Repo: "michael-denyer/pstack-claude"},
}

const (
	// Instructions is the file in agent-config that becomes ~/.claude/CLAUDE.md.
	Instructions = "instructions/AGENTS.md"
	// copyDir is where the sandbox keeps its copy of agent-config, relative to the agent account's home.
	copyDir = ".skybox/marketplaces/agent-config"
)

// MissingCloneError reports that the Mac has no agent-config clone to copy.
type MissingCloneError struct{ Dir string }

func (e *MissingCloneError) Error() string {
	return fmt.Sprintf("no agent-config clone at %s", e.Dir)
}

// CheckClone fails unless dir holds an agent-config clone with its instructions, so a sync changes nothing on the
// sandbox when the copy would be useless.
func CheckClone(dir string) error {
	if info, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) || (err == nil && !info.IsDir()) {
		return &MissingCloneError{Dir: dir}
	} else if err != nil {
		return err
	}
	instructions := filepath.Join(dir, Instructions)
	if info, err := os.Stat(instructions); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("the agent-config clone at %s has no %s to become CLAUDE.md", dir, Instructions)
	}
	return nil
}

// Archive writes dir as a tar stream: directories, regular files with their mode, and symlinks as they are. dir itself
// may be a symlink to the clone. Its .git comes along, because Claude Code takes a plugin's version from the commit
// when its manifest names none.
func Archive(w io.Writer, dir string) error {
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == dir {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var link string
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		case !info.IsDir() && !info.Mode().IsRegular():
			return nil
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			header.Name += "/"
		}
		// The agent account owns what it extracts, whoever owns it on the Mac.
		header.Uid, header.Gid, header.Uname, header.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

//go:embed sync.sh.tmpl
var scriptTemplate string

var script = template.Must(template.New("sync").Funcs(template.FuncMap{"quote": Quote}).Parse(scriptTemplate))

// Script is the bash script that syncs the toolkit on the sandbox, as the agent account, with Archive's stream of
// agent-config on stdin. Claude Code's output goes to stderr, and stdout carries only the final plugin list as JSON.
func Script() string {
	ids := make([]string, len(Plugins))
	for i, p := range Plugins {
		ids[i] = p.ID
	}
	keep, _ := json.Marshal(ids)
	var out bytes.Buffer
	if err := script.Execute(&out, map[string]any{
		"CopyDir":      copyDir,
		"Instructions": Instructions,
		"Plugins":      Plugins,
		"Keep":         string(keep),
	}); err != nil {
		panic(err)
	}
	return out.String()
}

// Quote makes s one word for a POSIX shell.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Installed is a plugin as `claude plugin list --json` reports it.
type Installed struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// CheckInstalled reads the plugin list Script prints. It fails unless the list is exactly Plugins, and returns it in
// their order.
func CheckInstalled(list []byte) ([]Installed, error) {
	var installed []Installed
	if err := json.Unmarshal(list, &installed); err != nil {
		return nil, fmt.Errorf("read the sandbox's plugin list: %w", err)
	}
	var want, got []string
	for _, p := range Plugins {
		want = append(want, p.ID)
	}
	for _, p := range installed {
		got = append(got, p.ID)
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		return nil, fmt.Errorf("the sandbox has the plugins %s, not %s", strings.Join(got, ", "), strings.Join(want, ", "))
	}
	ordered := make([]Installed, len(Plugins))
	for i, p := range Plugins {
		ordered[i] = installed[slices.IndexFunc(installed, func(in Installed) bool { return in.ID == p.ID })]
	}
	return ordered, nil
}
