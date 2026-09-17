package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// editorDoneMsg arrives when the editor e opened has exited and hunk has the
// terminal back.
type editorDoneMsg struct{ err error }

// openEditor hands the terminal to the user's editor on the file under the
// cursor, at the line the cursor is on. Only the working tree is on disk as it
// is on screen, so history and plain diffs have nothing to open.
func (m *Model) openEditor() tea.Cmd {
	if !m.staging() || m.cur >= len(m.view.Rows) {
		return nil
	}
	f := m.files[m.currentFile()]
	if f.IsDelete {
		return m.toast(f.Path() + " was deleted — nothing to edit")
	}
	editor := configuredEditor()
	if editor == "" {
		return m.toast("$VISUAL and $EDITOR are not set — export VISUAL=vim (or your editor) to edit from hunk")
	}
	root, err := m.repo.Root()
	if err != nil {
		return m.toast("edit failed: " + firstLine(err.Error()))
	}
	cmd := editorCmd(editor, filepath.Join(root, f.Path()), m.editLine())
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return editorDoneMsg{err} })
}

func configuredEditor() string {
	if editor := os.Getenv("VISUAL"); editor != "" {
		return editor
	}
	return os.Getenv("EDITOR")
}

// editorCmd runs editor on path at line. The editor goes through sh the same
// way git runs $VISUAL/$EDITOR, so it can carry its own arguments ("code --wait").
func editorCmd(editor, path string, line int) *exec.Cmd {
	n := strconv.Itoa(line)
	args := []string{"+" + n, path}
	switch editorName(editor) {
	case "code":
		args = []string{"--goto", path + ":" + n}
	case "hx", "subl", "zed":
		args = []string{path + ":" + n}
	}
	shellArgs := []string{"-c", editor + ` "$@"`, editor}
	return exec.Command("sh", append(shellArgs, args...)...)
}

func editorName(editor string) string {
	editor = strings.TrimSpace(editor)
	if editor == "" {
		return ""
	}
	if quote := editor[0]; quote == '\'' || quote == '"' {
		if end := strings.IndexByte(editor[1:], quote); end >= 0 {
			return filepath.Base(editor[1 : end+1])
		}
	}
	return filepath.Base(strings.Fields(editor)[0])
}

// editLine is the line of the new file the cursor points at. A removed line
// has no place in the new file, so it opens at the next line that does: where
// the change lands. A header opens at its first hunk the same way.
//
// ponytail: removed lines at the very end of a file, with nothing after them,
// open at line 1; fall back to the hunk's NewStart if that ever bothers anyone.
func (m *Model) editLine() int {
	file := m.view.Rows[m.cur].FileIdx
	for _, r := range m.view.Rows[m.cur:] {
		if r.FileIdx != file {
			break
		}
		if r.Right.Num > 0 {
			return r.Right.Num
		}
	}
	if file >= 0 && file < len(m.files) {
		if hunkIdx := m.view.Rows[m.cur].HunkIdx; hunkIdx >= 0 && hunkIdx < len(m.files[file].Hunks) {
			return m.files[file].Hunks[hunkIdx].NewStart
		}
	}
	return 1
}
