package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// editorDoneMsg arrives when the editor e opened has exited and hunk has the
// terminal back.
type editorDoneMsg struct {
	err      error
	editor   string
	path     string
	exitCode int
	elapsed  time.Duration
}

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
	started := time.Now()
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		exitCode := 0
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		return editorDoneMsg{
			err:      err,
			editor:   editor,
			path:     f.Path(),
			exitCode: exitCode,
			elapsed:  time.Since(started),
		}
	})
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

func editorDoneToast(msg editorDoneMsg) string {
	name := editorName(msg.editor)
	if msg.err != nil {
		if msg.exitCode == 127 && name != "" {
			return fmt.Sprintf("`%s` not found — check $VISUAL / $EDITOR", name)
		}
		if name == "" {
			name = "editor"
		}
		return name + ": " + firstLine(msg.err.Error())
	}
	if msg.elapsed < 500*time.Millisecond && (name == "code" || name == "subl" || name == "zed") && !strings.Contains(msg.editor, "--wait") {
		return fmt.Sprintf("`%s` returned immediately — try EDITOR='%s --wait'", name, msg.editor)
	}
	if msg.path != "" {
		return "reloaded " + msg.path
	}
	return ""
}

// editLine is the line of the new file the cursor points at. A removed line
// has no place in the new file, so it opens at the next line that does: where
// the change lands. A header opens at its first hunk the same way. Files with
// no hunks use line 1 as a safe editor fallback.
func (m *Model) editLine() int {
	row := m.view.Rows[m.cur]
	file := row.FileIdx
	for _, r := range m.view.Rows[m.cur:] {
		if r.FileIdx != file {
			break
		}
		if r.Right.Num > 0 {
			return r.Right.Num
		}
	}
	if row.HunkIdx < 0 || len(m.files[file].Hunks) == 0 {
		return 1
	}
	return m.files[file].Hunks[row.HunkIdx].NewStart
}
