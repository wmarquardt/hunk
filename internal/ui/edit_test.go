package ui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wmarquardt/hunk/internal/diff"
	"github.com/wmarquardt/hunk/internal/theme"
)

// rowWhere is the first row after from that satisfies ok, or -1.
func rowWhere(m *Model, from int, ok func(Row) bool) int {
	for i := from; i < len(m.view.Rows); i++ {
		if ok(m.view.Rows[i]) {
			return i
		}
	}
	return -1
}

func TestEditLineFollowsTheCursor(t *testing.T) {
	base := lines(40)
	m, _ := gitModelOpts(t,
		map[string]string{"a.txt": base},
		map[string]string{"a.txt": replaceLine(base, 20, "CHANGED")},
		Options{Unified: true},
	)

	m.moveTo(rowWhere(m, 0, func(r Row) bool { return r.Right.Kind == diff.Added }))
	if got := m.editLine(); got != 20 {
		t.Errorf("on the added line: line %d, want 20", got)
	}

	// A removed line is not in the new file; it opens where the change landed.
	m.moveTo(rowWhere(m, 0, func(r Row) bool { return r.Left.Kind == diff.Removed }))
	if got := m.editLine(); got != 20 {
		t.Errorf("on the removed line: line %d, want 20", got)
	}

	// A context line is its own line.
	m.moveTo(rowWhere(m, 0, func(r Row) bool { return r.Kind == RowPair && r.Right.Kind == diff.Context }))
	if got, want := m.editLine(), m.view.Rows[m.cur].Right.Num; got != want {
		t.Errorf("on a context line: line %d, want %d", got, want)
	}

	// The file header opens at the first hunk.
	m.moveTo(m.view.FileRows[0])
	if got, want := m.editLine(), m.files[0].Hunks[0].NewStart; got != want {
		t.Errorf("on the file header: line %d, want %d", got, want)
	}

	// Removed lines at the end of the file have no following new-file line.
	base = lines(40) + "tail\n"
	m, _ = gitModelOpts(t,
		map[string]string{"a.txt": base},
		map[string]string{"a.txt": lines(40)},
		Options{Unified: true},
	)
	m.moveTo(rowWhere(m, 0, func(r Row) bool { return r.Left.Num == 41 && r.Left.Kind == diff.Removed }))
	if got, want := m.editLine(), m.files[0].Hunks[0].NewStart; got != want {
		t.Errorf("on removed lines at end of file: line %d, want hunk start %d", got, want)
	}
}

func TestEditLineUsesSafeFallbackForHunklessFiles(t *testing.T) {
	files := []diff.File{
		{OldPath: "old.txt", NewPath: "new.txt", IsRename: true},
		{OldPath: "image.png", NewPath: "image.png", IsBinary: true},
	}
	m := New(files, theme.Default(), Options{})

	for _, fileRow := range m.view.FileRows {
		m.moveTo(fileRow)
		if got := m.editLine(); got != 1 {
			t.Errorf("on %s: line %d, want safe fallback 1", files[m.currentFile()].Path(), got)
		}
	}
}

func TestEditorCmdPassesEditorSpecificLineArgumentsThroughTheShell(t *testing.T) {
	tests := []struct {
		editor string
		args   []string
	}{
		{"vim", []string{"+12", "/repo/a.txt"}},
		{"emacs -nw", []string{"+12", "/repo/a.txt"}},
		{"emacsclient -t", []string{"+12", "/repo/a.txt"}},
		{"/usr/local/bin/hx", []string{"/repo/a.txt:12"}},
		{"code --wait", []string{"--goto", "/repo/a.txt:12"}},
		{`"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code" --wait`, []string{"--goto", "/repo/a.txt:12"}},
		{"/usr/local/bin/subl -w", []string{"/repo/a.txt:12"}},
		{"zed --wait", []string{"/repo/a.txt:12"}},
		{"unknown --flag", []string{"+12", "/repo/a.txt"}},
	}

	for _, tt := range tests {
		t.Run(tt.editor, func(t *testing.T) {
			cmd := editorCmd(tt.editor, "/repo/a.txt", 12)
			want := append([]string{"sh", "-c", tt.editor + ` "$@"`, tt.editor}, tt.args...)
			if !slices.Equal(cmd.Args, want) {
				t.Errorf("args = %q, want %q", cmd.Args, want)
			}
		})
	}
}

func TestConfiguredEditorPrefersVisual(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	t.Setenv("VISUAL", "code --wait")
	if got := configuredEditor(); got != "code --wait" {
		t.Errorf("editor = %q, want $VISUAL", got)
	}

	t.Setenv("VISUAL", "")
	if got := configuredEditor(); got != "vim" {
		t.Errorf("editor = %q, want $EDITOR", got)
	}
}

// The command really runs the configured editor with +line and the file: a
// stand-in editor records its arguments and edits the file.
func TestEditorCmdRunsTheEditor(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-editor")
	argsOut := filepath.Join(dir, "args")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" > "+argsOut+"\necho edited >> \"$2\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "file name.txt")
	if err := os.WriteFile(target, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := editorCmd(script, target, 7).CombinedOutput(); err != nil {
		t.Fatalf("editor failed: %v\n%s", err, out)
	}
	args, _ := os.ReadFile(argsOut)
	if got := strings.TrimSpace(string(args)); got != "+7 "+target {
		t.Errorf("editor got %q, want +7 and the path", got)
	}
	if b, _ := os.ReadFile(target); string(b) != "one\nedited\n" {
		t.Errorf("file = %q, want the editor's change", b)
	}
}

func TestEditOnlyInTheWorkingTree(t *testing.T) {
	plain := newTestModel(t, sample)
	screen(t, plain, 140, 20)
	if plain.command("E") != nil {
		t.Error("e opened an editor for a plain diff")
	}

	log, _ := logModel(t)
	screen(t, log, 140, 20)
	if log.command("E") != nil {
		t.Error("e opened an editor in history mode")
	}

	t.Setenv("EDITOR", "true")
	base := lines(10)
	m, _ := gitModel(t, map[string]string{"a.txt": base}, map[string]string{"a.txt": replaceLine(base, 3, "X")})
	if m.command("E") == nil || m.toastText != "" {
		t.Errorf("e did not open the editor in the working tree (toast %q)", m.toastText)
	}
}

// With no editor configured, e does not guess: a toast centered on the screen
// says what to set, and goes away on its own.
func TestEditWithoutEditorShowsAToast(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	base := lines(10)
	m, _ := gitModel(t, map[string]string{"a.txt": base}, map[string]string{"a.txt": replaceLine(base, 3, "X")})
	now := time.Now()
	m.clock = func() time.Time { return now }

	if m.command("E") == nil {
		t.Fatal("no tick to take the toast down")
	}
	lines := screen(t, m, 140, 24)
	if !strings.Contains(strings.Join(lines, "\n"), "$VISUAL and $EDITOR are not set") {
		t.Errorf("toast not on screen:\n%s", strings.Join(lines, "\n"))
	}
	if len(lines) != 24 {
		t.Errorf("toast changed the screen height to %d", len(lines))
	}

	// A tick for an older toast leaves a newer one up.
	now = now.Add(toastFor - time.Second)
	m.Update(toastDoneMsg{})
	if m.toastText == "" {
		t.Error("toast came down early")
	}
	now = now.Add(time.Second)
	m.Update(toastDoneMsg{})
	if m.toastText != "" || strings.Contains(strings.Join(screen(t, m, 140, 24), "\n"), "$VISUAL") {
		t.Error("toast stayed up past its time")
	}
}

func TestEditDeletedFileSaysSo(t *testing.T) {
	m, repo := gitModel(t, map[string]string{"gone.txt": "bye\n"}, nil)
	if err := os.Remove(filepath.Join(repo.Dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	m.liveReload()
	m.moveTo(0)

	m.command("E")
	if !strings.Contains(m.toastText, "deleted") {
		t.Errorf("toast = %q, want it to say the file is deleted", m.toastText)
	}
}

// Coming back from the editor re-reads the tree, keeps marks on untouched
// hunks and the cursor on its hunk, and reports an editor that failed.
func TestEditorDoneReloadsAndKeepsPlace(t *testing.T) {
	base := lines(60)
	edited := replaceLine(replaceLine(base, 5, "FIRST"), 30, "SECOND")
	m, repo := gitModel(t, map[string]string{"a.txt": base}, map[string]string{"a.txt": edited})

	m.moveTo(m.view.HunkRows[0] + 1)
	m.handleKey(keyPress(" "))
	m.moveTo(m.view.HunkRows[1] + 1)
	key := m.files[0].Hunks[1].Key()

	// What the user did in the editor.
	writeWorking(t, repo.Dir, "a.txt", replaceLine(edited, 50, "THIRD"))
	m.Update(editorDoneMsg{})

	if n := len(m.files[0].Hunks); n != 3 {
		t.Fatalf("after the edit: %d hunks, want 3", n)
	}
	if !strings.Contains(markedText(m), "FIRST") {
		t.Error("the mark on the untouched hunk did not survive")
	}
	if file, hunk := m.currentTarget(); hunk == wholeFile || m.files[file].Hunks[hunk].Key() != key {
		t.Error("cursor left the hunk it was on")
	}

	m.Update(editorDoneMsg{err: errors.New("exit status 1"), editor: "vim"})
	if !strings.Contains(m.toastText, "vim: exit status 1") {
		t.Errorf("toast = %q, want the editor's failure with its name", m.toastText)
	}

	m.Update(editorDoneMsg{err: errors.New("exit status 127"), editor: "hx", exitCode: 127})
	if !strings.Contains(m.toastText, "`hx` not found") {
		t.Errorf("toast = %q, want command-not-found guidance", m.toastText)
	}

	m.Update(editorDoneMsg{editor: "code", path: "a.txt", elapsed: 100 * time.Millisecond})
	if !strings.Contains(m.toastText, "code --wait") {
		t.Errorf("toast = %q, want wait-flag guidance", m.toastText)
	}

	m.Update(editorDoneMsg{editor: "vim", path: "a.txt", elapsed: time.Second})
	if !strings.Contains(m.toastText, "reloaded a.txt") {
		t.Errorf("toast = %q, want successful reload confirmation", m.toastText)
	}
}

func TestEditorDoneArmsMarqueeWhenNameOverflows(t *testing.T) {
	const path = "a_really_quite_long_file_name_indeed.go"
	m, repo := gitModel(t, map[string]string{path: "old\n"}, map[string]string{path: "new\n"})
	m.sideWidth = SidebarWidthMin
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})
	m = updated.(*Model)
	m.marqueeArmed = false
	writeWorking(t, repo.Dir, path, "newer\n")
	updated, cmd := m.Update(editorDoneMsg{})
	m = updated.(*Model)
	if !m.marqueeArmed {
		t.Fatal("editor return did not arm the marquee for a truncated name")
	}
	if cmd == nil {
		t.Fatal("editor return produced no marquee tick")
	}
}

func TestFsDirtyArmsMarqueeWhenNameOverflows(t *testing.T) {
	const path = "a_really_quite_long_file_name_indeed.go"
	m, repo := gitModel(t, map[string]string{path: "old\n"}, map[string]string{path: "new\n"})
	m.sideWidth = SidebarWidthMin
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})
	m = updated.(*Model)
	if !m.live || m.watch == nil {
		t.Fatal("git model should be live-following")
	}
	m.marqueeArmed = false
	writeWorking(t, repo.Dir, path, "newer\n")
	updated, cmd := m.Update(fsDirtyMsg{})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("fsDirty returned no command")
	}
	if !m.marqueeArmed {
		t.Fatal("live-follow reload did not arm the marquee for a truncated name")
	}
}
