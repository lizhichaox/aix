package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lizhichaox/aix/internal"
)

func showHarnessProviderMapping(harnessID, providerID string) error {
	spec, ok := internal.HarnessProvider(harnessID, providerID)
	if !ok {
		if _, err := internal.LoadHarnessRegistry(); err != nil {
			return err
		}
		return fmt.Errorf("provider %q has no %s harness mapping (edit %s with --edit)", providerID, harnessID, internal.HarnessRegistryPath(harnessID))
	}
	fmt.Printf("%s / %s mapping\n", harnessID, providerID)
	fmt.Printf("  API format:     %s\n", spec.APIFormat)
	fmt.Printf("  Base URL:       %s\n", spec.BaseURL)
	fmt.Printf("  Default model:  %s\n", spec.DefaultModel)
	fmt.Printf("  Default effort: %s\n", spec.DefaultEffort)
	fmt.Printf("  Source:         %s\n\n", harnessRegistrySource(harnessID))

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL KEY\tCLIENT MODEL\tUPSTREAM MODEL\tDEFAULT EFFORT\tEFFORTS")
	ids := make([]string, 0, len(spec.Models))
	for id := range spec.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		model := spec.Models[id]
		marker := ""
		if id == spec.DefaultModel {
			marker = " [default: " + spec.DefaultEffort + "]"
		}
		fmt.Fprintf(tw, "%s%s\t%s\t%s\t%s\t%s\n", id, marker, model.ClientModel, model.UpstreamModel, model.DefaultEffort, strings.Join(model.SupportedEfforts, ","))
	}
	return tw.Flush()
}

func harnessRegistrySource(harnessID string) string {
	path := internal.HarnessRegistryPath(harnessID)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return "AIX bundled defaults (run --edit to materialize)"
}

func editHarnessRegistry(harnessID, providerID, editorOverride string) error {
	path, err := internal.EnsureHarnessRegistryFile(harnessID)
	if err != nil {
		return err
	}
	backupPath := ""
	if data, readErr := os.ReadFile(path); readErr == nil {
		if mkdirErr := os.MkdirAll(internal.BackupsDir(), 0700); mkdirErr != nil {
			return mkdirErr
		}
		backupPath = filepath.Join(internal.BackupsDir(), filepath.Base(path)+".edit."+time.Now().Format("20060102-150405.000")+".bak")
		if writeErr := os.WriteFile(backupPath, data, 0600); writeErr != nil {
			return writeErr
		}
	}
	editor := resolveEditor(editorOverride)
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		return fmt.Errorf("VISUAL/EDITOR does not name an editor")
	}
	args := append(parts[1:], path)
	command := exec.Command(parts[0], args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("editor %q failed: %w", parts[0], err)
	}
	fmt.Printf("Harness registry saved: %s\n", path)
	if backupPath != "" {
		fmt.Printf("Backup: %s\n", backupPath)
	}
	return runHarnessDoctor(harnessID, providerID)
}

// resolveEditor picks the editor for --edit. An explicit --editor flag wins,
// then $VISUAL, then $EDITOR, then the "vi" fallback. A command line with
// arguments (e.g. "code --wait") is preserved after the first word.
func resolveEditor(editorOverride string) string {
	if e := strings.TrimSpace(editorOverride); e != "" {
		return e
	}
	if e := strings.TrimSpace(os.Getenv("VISUAL")); e != "" {
		return e
	}
	if e := strings.TrimSpace(os.Getenv("EDITOR")); e != "" {
		return e
	}
	return "vi"
}

func runHarnessDoctor(harnessID, providerID string) error {
	diagnostics := internal.DiagnoseHarnessRegistry(harnessID, providerID)
	if len(diagnostics) == 0 {
		target := harnessID
		if providerID != "" {
			target += "/" + providerID
		}
		fmt.Printf("✓ %s harness mapping is valid\n", target)
		return nil
	}
	errors := 0
	for _, diagnostic := range diagnostics {
		icon := "⚠"
		if diagnostic.Severity == "error" {
			icon = "✗"
			errors++
		}
		fmt.Printf("%s %s: %s\n", icon, diagnostic.Path, diagnostic.Reason)
		if diagnostic.Suggest != "" {
			fmt.Printf("  Suggestion: %s\n", diagnostic.Suggest)
		}
	}
	if errors > 0 {
		return fmt.Errorf("%s harness mapping has %d error(s)", harnessID, errors)
	}
	return nil
}

func validateHarnessAuxiliaryFlags(list, edit, doctor bool, argCount int, editor string) error {
	selected := 0
	for _, enabled := range []bool{list, edit, doctor} {
		if enabled {
			selected++
		}
	}
	if selected > 1 {
		return fmt.Errorf("--list, --edit, and --doctor are mutually exclusive")
	}
	if selected == 1 && argCount > 1 {
		return fmt.Errorf("harness mapping commands accept at most one provider argument")
	}
	if list && argCount == 0 {
		return fmt.Errorf("--list requires a provider argument")
	}
	if editor != "" && !edit {
		return fmt.Errorf("--editor requires --edit")
	}
	return nil
}
