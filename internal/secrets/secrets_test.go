package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempHomeNoMaind isolates HOME and removes maind from PATH so Store/Load
// exercise the .env fallback deterministically.
func withTempHomeNoMaind(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Join(home, "no-bin")) // maind not here
	for _, p := range ProviderOrder {
		t.Setenv(Providers[p], "")
	}
	return home
}

func TestStoreAndLoadDotenv(t *testing.T) {
	withTempHomeNoMaind(t)

	where, err := Store("anthropic", "sk-ant-123")
	if err != nil {
		t.Fatal(err)
	}
	if where != BackendDotenv {
		t.Fatalf("stored in %s, want .env", where)
	}

	path, _ := DotenvPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf(".env perms = %o, want 600", info.Mode().Perm())
	}

	// Source + Hydrate should surface the key.
	if Source("anthropic") != BackendDotenv {
		t.Errorf("source = %s", Source("anthropic"))
	}
	Hydrate()
	if os.Getenv("ANTHROPIC_API_KEY") != "sk-ant-123" {
		t.Errorf("hydrate didn't load key: %q", os.Getenv("ANTHROPIC_API_KEY"))
	}
}

func TestDotenvKeepsMultipleKeys(t *testing.T) {
	withTempHomeNoMaind(t)
	if _, err := Store("anthropic", "a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Store("openai", "o1"); err != nil {
		t.Fatal(err)
	}
	data := readDotenv()
	if data["ANTHROPIC_API_KEY"] != "a1" || data["OPENAI_API_KEY"] != "o1" {
		t.Fatalf("dotenv = %+v", data)
	}
}

func TestEnvVarWins(t *testing.T) {
	withTempHomeNoMaind(t)
	if _, err := Store("openai", "from-dotenv"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "from-env")
	if Source("openai") != BackendEnv {
		t.Errorf("env should win, got %s", Source("openai"))
	}
	Hydrate()
	if os.Getenv("OPENAI_API_KEY") != "from-env" {
		t.Error("hydrate must not override explicit env")
	}
}

func TestStoreUnknownProvider(t *testing.T) {
	withTempHomeNoMaind(t)
	if _, err := Store("bogus", "x"); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestReadDotenvIgnoresCommentsAndExport(t *testing.T) {
	home := withTempHomeNoMaind(t)
	dir := filepath.Join(home, ".config", "prismag")
	os.MkdirAll(dir, 0755)
	content := "# comment\nexport OPENAI_API_KEY=\"quoted\"\nANTHROPIC_API_KEY=plain\n"
	os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0600)
	d := readDotenv()
	if d["OPENAI_API_KEY"] != "quoted" || d["ANTHROPIC_API_KEY"] != "plain" {
		t.Fatalf("parsed = %+v", d)
	}
	if strings.Contains(d["OPENAI_API_KEY"], "\"") {
		t.Error("quotes should be stripped")
	}
}
