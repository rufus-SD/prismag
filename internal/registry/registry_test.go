package registry

import (
	"os"
	"path/filepath"
	"testing"
)

const validYAML = `
aliases:
  opus:
    model: claude-4.6-opus-high-thinking
    provider: anthropic
    description: Deep reasoning
  fast:
    model: gpt-5.3-codex
    provider: openai
`

func writeRegistry(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	r, err := Load(writeRegistry(t, validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Names()) != 2 {
		t.Fatalf("names = %d, want 2", len(r.Names()))
	}
	a, ok := r.Resolve("Opus")
	if !ok {
		t.Fatal("Resolve(Opus) = false")
	}
	if a.Model != "claude-4.6-opus-high-thinking" || a.Provider != ProviderAnthropic {
		t.Errorf("alias = %+v", a)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadNoAliases(t *testing.T) {
	_, err := Load(writeRegistry(t, "aliases: {}\n"))
	if err == nil {
		t.Fatal("expected error for empty aliases")
	}
}

func TestLoadMissingModel(t *testing.T) {
	yaml := `
aliases:
  opus:
    provider: anthropic
`
	_, err := Load(writeRegistry(t, yaml))
	if err == nil {
		t.Fatal("expected error for missing model")
	}
}

func TestLoadMissingProvider(t *testing.T) {
	yaml := `
aliases:
  opus:
    model: claude-4.6-opus-high-thinking
`
	_, err := Load(writeRegistry(t, yaml))
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestLoadUnknownProvider(t *testing.T) {
	yaml := `
aliases:
  opus:
    model: x
    provider: unknown-vendor
`
	_, err := Load(writeRegistry(t, yaml))
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestLoadBundledRegistry(t *testing.T) {
	// repo root registry.yaml — run from module root in CI/dev.
	path := filepath.Join("..", "..", "registry.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("bundled registry.yaml not found")
	}
	r, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Resolve("composer"); !ok {
		t.Fatal("expected composer alias")
	}
	if a, _ := r.Resolve("composer"); a.Provider != ProviderCursor {
		t.Errorf("composer provider = %q, want cursor", a.Provider)
	}
}

func TestDefaultYAMLIsValid(t *testing.T) {
	path := writeRegistry(t, string(DefaultYAML()))
	r, err := Load(path)
	if err != nil {
		t.Fatalf("embedded default registry is invalid: %v", err)
	}
	for _, want := range []string{"opus", "opus4.8", "composer", "gpt5.5"} {
		if _, ok := r.Resolve(want); !ok {
			t.Errorf("default registry missing alias %q", want)
		}
	}
}

func TestScaffoldGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, created, err := ScaffoldGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected created=true on first scaffold")
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("scaffolded registry invalid: %v", err)
	}

	_, created2, err := ScaffoldGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Error("second scaffold should not overwrite")
	}
}

func TestSuggest(t *testing.T) {
	r, err := Load(writeRegistry(t, validYAML))
	if err != nil {
		t.Fatal(err)
	}
	// validYAML defines opus + composer (see existing tests).
	got := r.Suggest("opux", 1)
	if len(got) != 1 || got[0] != "opus" {
		t.Errorf("Suggest(opux) = %v, want [opus]", got)
	}
	got = r.Suggest("xyz", 5)
	if len(got) == 0 {
		t.Error("Suggest should always return candidates")
	}
}

func TestDefaultPathWalksUp(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "registry.yaml"), []byte(validYAML), 0644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}

	oldWD, _ := os.Getwd()
	defer os.Chdir(oldWD)
	os.Chdir(sub)
	t.Setenv("PRISMAG_REGISTRY", "")

	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("walk-up failed: %v", err)
	}
	// Resolve symlinks (macOS /var -> /private/var) before comparing.
	gotDir, _ := filepath.EvalSymlinks(filepath.Dir(path))
	wantDir, _ := filepath.EvalSymlinks(root)
	if gotDir != wantDir {
		t.Errorf("found %q, want registry in %q", path, root)
	}
}

func TestNamesSorted(t *testing.T) {
	r, err := Load(writeRegistry(t, validYAML))
	if err != nil {
		t.Fatal(err)
	}
	names := r.Names()
	if names[0] != "fast" || names[1] != "opus" {
		t.Errorf("names = %v, want [fast opus]", names)
	}
}
