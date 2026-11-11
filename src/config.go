package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	configFile    = ".slurm/config"
	oldConfigFile = ".slurm.toml"
)

var errNoHost = errors.New("no host selected")

// Target is where lazyslurm works on a host, over ssh when Alias is set.
type Target struct {
	Alias string
	Dir   string
}

// Config is the .slurm/config of a repo resolved for one host.
type Config struct {
	Host     string
	Target   Target
	Push     []string
	Exclude  []string
	Keep     []string
	Pull     []string
	Scripts  []string
	Prelude  string
	Srun     string
	Sbatch   string
	PrePush  string
	PostPush string
	PrePull  string
	PostPull string
}

type table struct {
	Host     *string
	Target   *string
	Push     []string
	Exclude  []string
	Keep     []string
	Pull     []string
	Scripts  []string
	Prelude  *string
	Srun     *string
	Sbatch   *string
	PrePush  *string `toml:"pre_push"`
	PostPush *string `toml:"post_push"`
	PrePull  *string `toml:"pre_pull"`
	PostPull *string `toml:"post_pull"`
}

type file struct {
	Default table            `toml:"default"`
	Hosts   map[string]table `toml:"hosts"`
}

func parseConfig(root string) (file, error) {
	data, err := os.ReadFile(filepath.Join(root, configFile))
	if errors.Is(err, fs.ErrNotExist) {
		if _, oldErr := os.Stat(filepath.Join(root, oldConfigFile)); oldErr == nil {
			return file{}, fmt.Errorf("%s moved to %s: move it there", oldConfigFile, configFile)
		}
	}
	if err != nil {
		return file{}, fmt.Errorf("read %s: %w", configFile, err)
	}
	var f file
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return file{}, fmt.Errorf("%s: %w", configFile, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return file{}, fmt.Errorf("%s: unknown key %s", configFile, keys[0])
	}
	if f.Default.Target != nil {
		return file{}, errors.New("target: only allowed in a [hosts.NAME] table")
	}
	for name, t := range f.Hosts {
		if t.Host != nil {
			return file{}, fmt.Errorf("hosts.%s.host: only allowed in [default]", name)
		}
	}
	return f, nil
}

func loadConfig(root, hostFlag string) (Config, error) {
	f, err := parseConfig(root)
	if err != nil {
		return Config{}, err
	}
	name, err := selectHost(f, hostFlag)
	if err != nil {
		return Config{}, err
	}
	cfg, err := merge(name, f.Default, f.Hosts[name])
	if err != nil {
		return Config{}, err
	}
	return cfg, validate(&cfg)
}

// hostList returns the hosts of .slurm/config and the selected one, the first when none is.
func hostList(root, hostFlag string) ([]string, string, error) {
	f, err := parseConfig(root)
	if err != nil {
		return nil, "", err
	}
	names := hostNames(f)
	name, err := selectHost(f, hostFlag)
	if errors.Is(err, errNoHost) && len(names) > 0 {
		name, err = names[0], nil
	}
	return names, name, err
}

func hostNames(f file) []string {
	names := make([]string, 0, len(f.Hosts))
	for name := range f.Hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func selectHost(f file, hostFlag string) (string, error) {
	name := hostFlag
	if name == "" {
		name = os.Getenv("CLUSTER")
	}
	if name == "" && f.Default.Host != nil {
		name = *f.Default.Host
	}
	names := hostNames(f)
	if name == "" && len(names) == 1 {
		name = names[0]
	}
	if name == "" {
		return "", fmt.Errorf("%w: use -H (hosts: %s)", errNoHost, strings.Join(names, ", "))
	}
	if _, ok := f.Hosts[name]; !ok {
		return "", fmt.Errorf("unknown host %q (hosts: %s)", name, strings.Join(names, ", "))
	}
	return name, nil
}

func merge(name string, def, host table) (Config, error) {
	pick := func(h, d *string) string {
		if h != nil {
			return *h
		}
		if d != nil {
			return *d
		}
		return ""
	}
	list := func(h, d []string) []string {
		if h != nil {
			return h
		}
		return d
	}
	if host.Target == nil {
		return Config{}, fmt.Errorf("hosts.%s.target: missing", name)
	}
	target, err := parseTarget(*host.Target)
	if err != nil {
		return Config{}, fmt.Errorf("hosts.%s.target: %w", name, err)
	}
	return Config{
		Host:     name,
		Target:   target,
		Push:     list(host.Push, def.Push),
		Exclude:  list(host.Exclude, def.Exclude),
		Keep:     list(host.Keep, def.Keep),
		Pull:     list(host.Pull, def.Pull),
		Scripts:  list(host.Scripts, def.Scripts),
		Prelude:  pick(host.Prelude, def.Prelude),
		Srun:     pick(host.Srun, def.Srun),
		Sbatch:   pick(host.Sbatch, def.Sbatch),
		PrePush:  pick(host.PrePush, def.PrePush),
		PostPush: pick(host.PostPush, def.PostPush),
		PrePull:  pick(host.PrePull, def.PrePull),
		PostPull: pick(host.PostPull, def.PostPull),
	}, nil
}

func parseTarget(s string) (Target, error) {
	colon := strings.Index(s, ":")
	slash := strings.Index(s, "/")
	if colon > 0 && (slash < 0 || colon < slash) {
		if colon == len(s)-1 {
			return Target{}, fmt.Errorf("%q has no directory", s)
		}
		return Target{Alias: s[:colon], Dir: s[colon+1:]}, nil
	}
	if !filepath.IsAbs(s) && !strings.HasPrefix(s, "~/") {
		return Target{}, fmt.Errorf("%q must be absolute or start with ~/", s)
	}
	return Target{Dir: s}, nil
}

func inside(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+"/")
}

func badPath(p string) bool {
	if p == "" || filepath.IsAbs(p) || filepath.Clean(p) == "." {
		return true
	}
	return slices.Contains(strings.Split(p, "/"), "..")
}

func validate(c *Config) error {
	if len(c.Push) == 0 {
		return errors.New("push: missing or empty")
	}
	for _, key := range []struct {
		name  string
		paths *[]string
	}{{"push", &c.Push}, {"keep", &c.Keep}, {"pull", &c.Pull}} {
		for i, p := range *key.paths {
			if badPath(p) {
				return fmt.Errorf("%s: %q must be a path inside the repo", key.name, p)
			}
			(*key.paths)[i] = filepath.Clean(p)
		}
	}
	shared := append(append([]string{}, c.Push...), c.Keep...)
	for i, a := range shared {
		for _, b := range shared[i+1:] {
			if inside(a, b) || inside(b, a) {
				return fmt.Errorf("push and keep: %q and %q overlap", a, b)
			}
		}
	}
	for _, p := range c.Pull {
		if !insideAny(p, c.Keep) {
			return fmt.Errorf("pull: %q is not inside a keep path", p)
		}
	}
	for _, e := range c.Exclude {
		if strings.Contains(e, "/") {
			return fmt.Errorf("exclude: %q contains a slash", e)
		}
		if _, err := filepath.Match(e, ""); err != nil {
			return fmt.Errorf("exclude: %q: %w", e, err)
		}
	}
	return nil
}

func insideAny(path string, dirs []string) bool {
	for _, d := range dirs {
		if inside(path, d) {
			return true
		}
	}
	return false
}
