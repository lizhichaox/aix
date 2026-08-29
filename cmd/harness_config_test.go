package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lizhichaox/aix/internal"
)

func TestShowHarnessProviderMapping(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out := captureStdout(t, func() {
		if err := showHarnessProviderMapping(internal.HarnessClaude, "openrouter"); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"claude / openrouter mapping", "Default model:", "Default effort: high", "CLIENT MODEL", "UPSTREAM MODEL"} {
		if !strings.Contains(out, want) {
			t.Errorf("mapping output missing %q:\n%s", want, out)
		}
	}
}

func TestEditHarnessRegistryMaterializesDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VISUAL", "/usr/bin/true")
	t.Setenv("EDITOR", "")
	if err := editHarnessRegistry(internal.HarnessCodex, "openrouter", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(internal.HarnessRegistryPath(internal.HarnessCodex)); err != nil {
		t.Fatalf("editable registry not created: %v", err)
	}
	if _, err := internal.LoadHarnessRegistry(); err != nil {
		t.Fatalf("materialized registry is invalid: %v", err)
	}
	backups, err := os.ReadDir(internal.BackupsDir())
	if err != nil || len(backups) == 0 {
		t.Fatalf("edit backup missing: entries=%v err=%v", backups, err)
	}
}

func TestHarnessDoctorReturnsActionableError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(internal.HarnessRegistryPath(internal.HarnessCodex)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(internal.HarnessRegistryPath(internal.HarnessCodex), []byte("version = 1\n[providers.bad\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := runHarnessDoctor(internal.HarnessCodex, "bad"); err == nil {
			t.Fatal("doctor should fail malformed TOML")
		}
	})
	if !strings.Contains(out, "Suggestion:") || !strings.Contains(out, "TOML") {
		t.Errorf("doctor output is not actionable:\n%s", out)
	}
}

func TestResolveEditorPrecedence(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if got := resolveEditor(""); got != "vi" {
		t.Errorf("default editor = %q, want vi", got)
	}
	if got := resolveEditor("zed"); got != "zed" {
		t.Errorf("override editor = %q, want zed", got)
	}
	t.Setenv("VISUAL", "subl")
	t.Setenv("EDITOR", "code")
	if got := resolveEditor(""); got != "subl" {
		t.Errorf("VISUAL precedence = %q, want subl", got)
	}
	t.Setenv("VISUAL", "")
	if got := resolveEditor(""); got != "code" {
		t.Errorf("EDITOR fallback = %q, want code", got)
	}
}

func TestEditorRequiresEdit(t *testing.T) {
	if err := validateHarnessAuxiliaryFlags(false, false, false, 0, "code"); err == nil {
		t.Fatal("--editor without --edit should error")
	}
	if err := validateHarnessAuxiliaryFlags(false, true, false, 1, "code"); err != nil {
		t.Fatalf("--edit with --editor should succeed: %v", err)
	}
}

func TestEditHarnessRegistryUsesEditorOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if err := editHarnessRegistry(internal.HarnessCodex, "openrouter", "/usr/bin/true"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(internal.HarnessRegistryPath(internal.HarnessCodex)); err != nil {
		t.Fatalf("editable registry not created: %v", err)
	}
}
