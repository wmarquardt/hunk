package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wmarquardt/hunk/internal/diff"
	"github.com/wmarquardt/hunk/internal/git"
	"github.com/wmarquardt/hunk/internal/theme"
)

// NewLog builds a model over a repository's history. It is NewGit without the
// index: a commit is something to read, so marking, staging and following the
// working tree are all off, and nothing here can write.
func NewLog(repo *git.Repo, commits []git.Commit, files []diff.File, t *theme.Theme, opts Options) *Model {
	m := New(files, t, opts)
	m.repo = repo
	m.commits = commits
	m.logMode = true
	return m
}

// RunLog puts the history on screen and blocks until the user quits.
func RunLog(repo *git.Repo, commits []git.Commit, files []diff.File, t *theme.Theme, opts Options) error {
	_, err := tea.NewProgram(NewLog(repo, commits, files, t, opts)).Run()
	return err
}

// commit is the commit currently on screen.
func (m *Model) commit() git.Commit {
	if m.commitIdx < 0 || m.commitIdx >= len(m.commits) {
		return git.Commit{}
	}
	return m.commits[m.commitIdx]
}

// loadCommit moves to another commit and shows it from the top. Indexes clamp
// instead of wrapping, so } on the oldest commit stays there.
func (m *Model) loadCommit(idx int) {
	if !m.logMode || len(m.commits) == 0 {
		return
	}
	idx = min(max(idx, 0), len(m.commits)-1)
	if idx == m.commitIdx {
		return
	}
	m.commitIdx = idx
	if m.showCommit() {
		m.cur, m.top, m.hscroll = 0, 0, 0
	}
}

// showCommit reads the current commit into the view. It reports whether the
// read worked; a failure leaves what is on screen alone and says so.
func (m *Model) showCommit() bool {
	text, err := m.repo.Show(m.commit().SHA, m.ignoreWS, m.context)
	var files []diff.File
	if err == nil {
		files, err = diff.ParseString(text)
	}
	if err != nil {
		m.msg = "show failed: " + firstLine(err.Error())
		return false
	}
	m.raw = files
	m.applyFilter()
	m.rebuildView()
	return true
}

// rediff re-runs whatever produced the diff on screen, keeping the user's
// place. It is what + / - and i do once they have changed what to ask for.
func (m *Model) rediff() {
	if m.logMode {
		where := m.cursorIdentity()
		if m.showCommit() {
			m.restoreCursor(where)
		}
		return
	}
	m.liveReload()
}

// logSplit is how many sidebar rows the commit list gets. Each list also costs
// a header row, and the file list below takes whatever is left over.
func (m *Model) logSplit(h int) int {
	avail := h - 2
	if avail < 2 {
		return 0
	}
	return min(avail/2, len(m.commits))
}

// renderLogSidebar draws the two stacked lists of history mode: the commits,
// then the files of the selected one.
func (m *Model) renderLogSidebar(h, w int) []string {
	cn := m.logSplit(h)
	out := make([]string, 0, h)

	focus := m.panelFocus()
	out = append(out, m.sidebarHeader("Commits", w, focus == focusCommits))
	start := listStart(m.commitIdx, cn)
	for i := 0; i < cn; i++ {
		out = append(out, m.commitLine(start+i, w))
	}

	out = append(out, m.sidebarHeader("Files", w, focus == focusTree))
	return append(out, m.renderTree(h-cn-2, w)...)
}

// logSidebarAt maps a sidebar row to what is drawn there: a commit, a file, or
// neither. It mirrors renderLogSidebar so a click lands on what it points at.
func (m *Model) logSidebarAt(y int) (commit, file int) {
	h := m.bodyHeight()
	cn := m.logSplit(h)
	switch {
	case y == 0 || y == cn+1: // a header
	case y <= cn:
		if idx := listStart(m.commitIdx, cn) + y - 1; idx < len(m.commits) {
			return idx, -1
		}
	default:
		if idx := m.treeFileAt(y-cn-2, h-cn-2); idx >= 0 {
			return -1, idx
		}
	}
	return -1, -1
}

// commitLine renders one row of the commit list, or a blank row past the end.
func (m *Model) commitLine(idx, w int) string {
	if idx < 0 || idx >= len(m.commits) {
		return m.st.sidebar.Render(strings.Repeat(" ", w))
	}
	c := m.commits[idx]
	style := m.st.sidebar
	if idx == m.commitIdx {
		style = m.st.sidebarSel
	}
	subjW := commitSubjectWidth(w, c.Short)
	subj := m.clipOrMarquee("commit:"+c.SHA, c.Subject, subjW, idx == m.commitIdx)
	label := fmt.Sprintf(" %s  %s", c.Short, subj)
	return fit(style.Render(label), 0, w, style)
}

// sidebarHeader titles one of the two lists, in the same border color as the
// rule that separates the sidebar from the diff, or in the accent while that
// list has focus.
func (m *Model) sidebarHeader(label string, w int, focused bool) string {
	style := m.st.gutter
	if focused {
		style = m.st.focusRail
	}
	text := "── " + label + " "
	if pad := w - lipgloss.Width(text); pad > 0 {
		text += strings.Repeat("─", pad)
	}
	return fit(style.Render(text), 0, w, m.st.gutter)
}

// clip shortens text from the right, which is where a commit subject or a
// file name in the tree gets less informative.
func clip(s string, w int) string { return ansi.Truncate(s, w, "…") }

// marqueeHold is how many frames the selected name sits still at each end
// (~2s at marqueeStep). marqueeStep is one column of scroll.
const (
	marqueeHold = 11
	marqueeStep = 180 * time.Millisecond
)

// marqueeTickMsg is one frame of the selected-row name scroll.
type marqueeTickMsg struct{}

// marquee returns the window of s visible at frame, for a name that does not
// fit in w columns. A name that fits is unchanged on every frame. Hold frames
// at each end show the same window; the last scroll frame shows the end of the
// name; wrapping returns to frame 0's output.
func marquee(s string, w, frame int) string {
	if w <= 0 {
		return ""
	}
	sw := ansi.StringWidth(s)
	if sw <= w {
		return s
	}
	maxOff := sw - w
	cycle := 2*marqueeHold + maxOff
	if frame < 0 {
		frame = 0
	}
	f := frame % cycle
	var off int
	switch {
	case f < marqueeHold:
		off = 0
	case f < marqueeHold+maxOff:
		off = f - marqueeHold + 1
	default:
		off = maxOff
	}
	return ansi.Cut(s, off, off+w)
}

// clipOrMarquee clips an unselected name with an ellipsis, and scrolls a
// selected name that does not fit. Each key keeps its own frame so a commit
// subject and a file name in log mode can both move. A key that was not drawn
// this render is dropped, so selecting it again starts at the beginning.
func (m *Model) clipOrMarquee(key, s string, w int, sel bool) string {
	if !sel {
		return clip(s, w)
	}
	if m.marqueeFrames == nil {
		m.marqueeFrames = map[string]int{}
	}
	if m.marqueeSeen == nil {
		m.marqueeSeen = map[string]bool{}
	}
	m.marqueeSeen[key] = true
	if _, ok := m.marqueeFrames[key]; !ok {
		m.marqueeFrames[key] = 0
	}
	if w > 0 && ansi.StringWidth(s) > w {
		m.marqueeOverflow = true
	}
	return marquee(s, w, m.marqueeFrames[key])
}

func (m *Model) pruneMarquee() {
	for k := range m.marqueeFrames {
		if !m.marqueeSeen[k] {
			delete(m.marqueeFrames, k)
		}
	}
	clear(m.marqueeSeen)
}

// commitSubjectWidth is the columns a commit subject may occupy in a sidebar
// of width w. commitLine and the overflow check share it so a later layout
// change cannot leave them computing different rooms.
func commitSubjectWidth(w int, short string) int {
	return w - lipgloss.Width(short) - 3
}

// treeNameRoom is the columns a tree row's name may occupy in a sidebar of
// width w. treeRow and the overflow check share it so the clock and the
// rendered width cannot drift.
func (m *Model) treeNameRoom(l treeLine, w int) int {
	if l.file < 0 {
		return w - lipgloss.Width(" "+l.prefix+"/")
	}
	f := m.files[l.file]
	adds := fmt.Sprintf("+%d", f.Added)
	dels := fmt.Sprintf(" -%d", f.Removed)
	lead := " " + l.prefix
	if m.staging() {
		symbol, _ := m.fileGlyph(l.file, f, m.st.sidebar)
		lead += symbol + " "
	}
	return max(w-lipgloss.Width(lead)-lipgloss.Width(adds+dels)-2, 1)
}

// selectedNameOverflows reports whether a selected sidebar row's name does not
// fit, the only case that needs the marquee clock. Keys and resizes use this
// before the next paint; ticks reuse marqueeOverflow from the last paint.
func (m *Model) selectedNameOverflows() bool {
	if !m.sidebar() {
		return false
	}
	w := m.sidebarW()
	if m.logMode && len(m.commits) > 0 {
		c := m.commit()
		if ansi.StringWidth(c.Subject) > commitSubjectWidth(w, c.Short) {
			return true
		}
	}
	tree := buildTree(m.files, m.collapsed)
	sel := m.treeSel(tree)
	if sel < 0 || sel >= len(tree) {
		return false
	}
	return m.treeNameOverflows(tree[sel], w)
}

func (m *Model) treeNameOverflows(l treeLine, w int) bool {
	return ansi.StringWidth(l.name) > m.treeNameRoom(l, w)
}
