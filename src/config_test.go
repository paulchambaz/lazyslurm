package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const validConfig = `
[default]
host    = "pfcalcul"
push    = ["src", "assets", "slurm", "pyproject.toml", "uv.lock"]
exclude = ["__pycache__"]
keep    = [".venv", "outputs"]
pull    = ["outputs/logs", "outputs/weights"]
scripts = ["slurm/*.sh"]
prelude = "source .venv/bin/activate"
post_push = "bash slurm/setup.sh"

[hosts.pfcalcul]
target = "pfcalcul:rl-algos"
srun   = "--partition=gpu"

[hosts.here]
target = "/scratch/paul/rl-algos"
push   = ["src"]
post_push = ""
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, configFile), body)
	return root
}

func TestLoadConfigDefaultHost(t *testing.T) {
	t.Setenv("CLUSTER", "")
	got, err := loadConfig(writeConfig(t, validConfig), "")
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Host:     "pfcalcul",
		Target:   Target{Alias: "pfcalcul", Dir: "rl-algos"},
		Push:     []string{"src", "assets", "slurm", "pyproject.toml", "uv.lock"},
		Exclude:  []string{"__pycache__"},
		Keep:     []string{".venv", "outputs"},
		Pull:     []string{"outputs/logs", "outputs/weights"},
		Scripts:  []string{"slurm/*.sh"},
		Prelude:  "source .venv/bin/activate",
		Srun:     "--partition=gpu",
		PostPush: "bash slurm/setup.sh",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestHostOverridesReplaceWhole(t *testing.T) {
	t.Setenv("CLUSTER", "")
	got, err := loadConfig(writeConfig(t, validConfig), "here")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Push, []string{"src"}) {
		t.Errorf("push = %v", got.Push)
	}
	if got.PostPush != "" {
		t.Errorf("post_push = %q, want it overridden by an empty string", got.PostPush)
	}
	if got.Target != (Target{Dir: "/scratch/paul/rl-algos"}) {
		t.Errorf("target = %+v", got.Target)
	}
	if !reflect.DeepEqual(got.Keep, []string{".venv", "outputs"}) {
		t.Errorf("keep = %v, want the default", got.Keep)
	}
}

func TestSelectHostOrder(t *testing.T) {
	root := writeConfig(t, validConfig)
	t.Setenv("CLUSTER", "here")
	got, err := loadConfig(root, "")
	if err != nil || got.Host != "here" {
		t.Fatalf("CLUSTER: host = %q, err = %v", got.Host, err)
	}
	got, err = loadConfig(root, "pfcalcul")
	if err != nil || got.Host != "pfcalcul" {
		t.Fatalf("-H over CLUSTER: host = %q, err = %v", got.Host, err)
	}
}

func TestSelectHostOnlyHost(t *testing.T) {
	t.Setenv("CLUSTER", "")
	root := writeConfig(t, "[default]\npush = [\"src\"]\n[hosts.a]\ntarget = \"/x\"\n")
	got, err := loadConfig(root, "")
	if err != nil || got.Host != "a" {
		t.Fatalf("host = %q, err = %v", got.Host, err)
	}
}

func TestSelectHostErrors(t *testing.T) {
	t.Setenv("CLUSTER", "")
	root := writeConfig(t, strings.Replace(validConfig, `host    = "pfcalcul"`, "", 1))
	_, err := loadConfig(root, "")
	if !strings.Contains(err.Error(), "here, pfcalcul") {
		t.Errorf("no host: %v", err)
	}
	_, err = loadConfig(root, "nope")
	if !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), "here, pfcalcul") {
		t.Errorf("unknown host: %v", err)
	}
}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		in   string
		want Target
		bad  bool
	}{
		{"pfcalcul:rl-algos", Target{"pfcalcul", "rl-algos"}, false},
		{"pfcalcul:~/rl", Target{"pfcalcul", "~/rl"}, false},
		{"pfcalcul:/abs/dir", Target{"pfcalcul", "/abs/dir"}, false},
		{"/scratch/x", Target{"", "/scratch/x"}, false},
		{"~/x", Target{"", "~/x"}, false},
		{"/dir/with:colon", Target{"", "/dir/with:colon"}, false},
		{"relative", Target{}, true},
		{"alias:", Target{}, true},
		{"", Target{}, true},
	}
	for _, tt := range tests {
		got, err := parseTarget(tt.in)
		if (err != nil) != tt.bad || got != tt.want {
			t.Errorf("parseTarget(%q) = %+v, %v", tt.in, got, err)
		}
	}
}

func TestConfigErrors(t *testing.T) {
	const host = "[hosts.a]\ntarget = \"/x\"\n"
	tests := []struct {
		name string
		body string
		key  string
	}{
		{"unknown key", "[default]\npush = [\"src\"]\nbogus = 1\n" + host, "bogus"},
		{"wrong type", "[default]\npush = \"src\"\n" + host, "push"},
		{"missing push", "[default]\n" + host, "push"},
		{"empty push", "[default]\npush = []\n" + host, "push"},
		{"missing target", "[default]\npush = [\"src\"]\n[hosts.a]\n", "target"},
		{"target in default", "[default]\npush = [\"src\"]\ntarget = \"/x\"\n" + host, "target"},
		{"host in host", "[default]\npush = [\"src\"]\n[hosts.a]\ntarget = \"/x\"\nhost = \"a\"\n", "host"},
		{"absolute path", "[default]\npush = [\"/src\"]\n" + host, "push"},
		{"dotdot path", "[default]\npush = [\"a/../b\"]\n" + host, "push"},
		{"dot path", "[default]\npush = [\".\"]\n" + host, "push"},
		{"equal push and keep", "[default]\npush = [\"src\"]\nkeep = [\"src\"]\n" + host, "keep"},
		{"nested push and keep", "[default]\npush = [\"src\"]\nkeep = [\"src/data\"]\n" + host, "keep"},
		{"pull outside keep", "[default]\npush = [\"src\"]\nkeep = [\"out\"]\npull = [\"logs\"]\n" + host, "pull"},
		{"exclude bad glob", "[default]\npush = [\"src\"]\nexclude = [\"[\"]\n" + host, "exclude"},
		{"exclude slash", "[default]\npush = [\"src\"]\nexclude = [\"a/b\"]\n" + host, "exclude"},
	}
	t.Setenv("CLUSTER", "")
	for _, tt := range tests {
		_, err := loadConfig(writeConfig(t, tt.body), "")
		if err == nil || !strings.Contains(err.Error(), tt.key) {
			t.Errorf("%s: err = %v, want it to name %q", tt.name, err, tt.key)
		}
	}
}

func TestPathsAreCleaned(t *testing.T) {
	t.Setenv("CLUSTER", "")
	root := writeConfig(t, "[default]\npush = [\"src/\", \"./a\"]\n[hosts.a]\ntarget = \"/x\"\n")
	got, err := loadConfig(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Push, []string{"src", "a"}) {
		t.Errorf("push = %v", got.Push)
	}
}

func TestMissingConfigFile(t *testing.T) {
	if _, err := loadConfig(t.TempDir(), ""); err == nil || !strings.Contains(err.Error(), configFile) {
		t.Errorf("err = %v", err)
	}
}

func TestOldConfigLocationIsReported(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, oldConfigFile), validConfig)
	if _, err := loadConfig(root, ""); err == nil || !strings.Contains(err.Error(), "move it there") {
		t.Errorf("err = %v", err)
	}
}

func TestAllowDirtyDefaultsToFalseAndCanBeSetPerHost(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		want       bool
	}{
		{"default", "", false},
		{"default table", "allow_dirty = true\n", true},
		{"host overrides", "allow_dirty = true\n", false},
	} {
		root := t.TempDir()
		host := "target = \"/x\"\n"
		if tt.name == "host overrides" {
			host += "allow_dirty = false\n"
		}
		write(t, filepath.Join(root, configFile), "[default]\npush = [\"src\"]\n"+tt.body+"[hosts.h]\n"+host)
		cfg, err := loadConfig(root, "")
		if err != nil || cfg.AllowDirty != tt.want {
			t.Errorf("%s: allow_dirty = %v, %v, want %v", tt.name, cfg.AllowDirty, err, tt.want)
		}
	}
}
