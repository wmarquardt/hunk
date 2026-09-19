package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wmarquardt/hunk/internal/diff"
	"github.com/wmarquardt/hunk/internal/theme"
)

// treeDiff is a diff touching each path once, one line changed per file, in the
// order given — deliberately not tree order.
func treeDiff(paths ...string) string {
	var b strings.Builder
	for _, p := range paths {
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -1,1 +1,1 @@\n-old\n+new\n", p, p, p, p)
	}
	return b.String()
}

var treePaths = []string{"b.go", "a.go", "a/z.go", "README", "a/b/c.go"}

func filePaths(m *Model) []string {
	var out []string
	for _, f := range m.files {
		out = append(out, f.Path())
	}
	return out
}

func TestComparePaths(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"install.sh", "internal/ui/tree.go", 1},
		{"internal/ui/tree.go", "install.sh", -1},
		{"main.go", "internal/ui/tree.go", 1},
		{"internal/ui/tree.go", "main.go", -1},
		{"a/b.go", "a.go", -1},
		{"a.go", "a/b.go", 1},
		{"a.go", "b.go", -1},
		{"b.go", "a.go", 1},
		{"a/x.go", "b/x.go", -1},
		{"b/x.go", "a/x.go", 1},
		{"a/b/c.go", "a/z.go", -1},
		{"a/z.go", "a/b/c.go", 1},
		{"a/b/c.go", "a/b/d.go", -1},
		{"same.go", "same.go", 0},
	}
	sign := func(n int) int {
		if n < 0 {
			return -1
		}
		if n > 0 {
			return 1
		}
		return 0
	}
	for _, tt := range tests {
		if got := sign(comparePaths(tt.a, tt.b)); got != sign(tt.want) {
			t.Errorf("comparePaths(%q, %q) = %d, want sign of %d", tt.a, tt.b, comparePaths(tt.a, tt.b), tt.want)
		}
	}
}

func TestFilesAreInTreeOrder(t *testing.T) {
	m := newTestModel(t, treeDiff(treePaths...))
	want := []string{"a/b/c.go", "a/z.go", "README", "a.go", "b.go"}
	if got := filePaths(m); !slices.Equal(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
}

func TestBuildTreeDrawsConnectors(t *testing.T) {
	m := newTestModel(t, treeDiff(treePaths...))
	tree := buildTree(m.files, nil)

	want := []string{
		"├─a",
		"│ ├─b",
		"│ │ └─c.go",
		"│ └─z.go",
		"├─README",
		"├─a.go",
		"└─b.go",
	}
	var got []string
	for _, l := range tree {
		got = append(got, l.prefix+l.name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tree =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Directories hold the run of files beneath them and are never a file.
	if a := tree[0]; a.file != -1 || a.lo != 0 || a.hi != 2 {
		t.Errorf("a/ = file %d [%d,%d), want a directory over [0,2)", a.file, a.lo, a.hi)
	}
	if b := tree[1]; b.file != -1 || b.lo != 0 || b.hi != 1 {
		t.Errorf("a/b/ = file %d [%d,%d), want a directory over [0,1)", b.file, b.lo, b.hi)
	}
	if c := tree[2]; c.file != 0 {
		t.Errorf("c.go points at file %d, want 0", c.file)
	}
}

func TestSidebarDrawsTheTree(t *testing.T) {
	m := newTestModel(t, treeDiff(treePaths...))
	lines := screen(t, m, 120, 12)

	for i, want := range []string{"├─a/", "│ ├─b/", "│ │ └─c.go", "└─b.go"} {
		row := []int{0, 1, 2, 6}[i]
		if !strings.Contains(lines[row], want) {
			t.Errorf("sidebar row %d = %q, want it to contain %q", row, lines[row], want)
		}
	}
	// The counts hug the right edge of the sidebar column.
	for _, row := range []int{3, 6} {
		side := string([]rune(lines[row])[:sidebarWidth])
		if !strings.HasSuffix(side, "+1 -1") {
			t.Errorf("sidebar row %d = %q, want counts flush right", row, side)
		}
	}
}

func TestTreeFillsTheScreenExactly(t *testing.T) {
	m := newTestModel(t, treeDiff(append(treePaths, "deep/er/and/deeper/still/a_rather_long_file_name.go")...))
	for _, size := range [][2]int{{100, 5}, {120, 30}, {200, 50}} {
		m.focus = focusTree
		for _, sw := range []int{SidebarWidthMin, SidebarWidthMax} {
			m.sideWidth = sw
			w, h := size[0], size[1]
			lines := screen(t, m, w, h)
			if len(lines) != h {
				t.Errorf("%dx%d sidebar %d: %d lines, want %d", w, h, sw, len(lines), h)
			}
			for i, l := range lines {
				if got := ansi.StringWidth(l); got != w && i < h-1 {
					t.Errorf("%dx%d sidebar %d: line %d is %d wide, want %d: %q", w, h, sw, i, got, w, l)
				}
			}
		}
	}
}

func TestTreeTruncatesNamesButKeepsCounts(t *testing.T) {
	m := newTestModel(t, treeDiff(
		"a_really_quite_long_file_name_indeed.go",
		"second_also_quite_long_file_name.go",
	))
	m.sideWidth = SidebarWidthMin
	lines := screen(t, m, 120, 6)
	sel := ansi.Cut(lines[0], 0, SidebarWidthMin)
	other := ansi.Cut(lines[1], 0, SidebarWidthMin)
	if strings.Contains(sel, "…") {
		t.Errorf("selected row = %q, want a window not an ellipsis", sel)
	}
	if !strings.HasSuffix(strings.TrimRight(sel, " "), "+1 -1") {
		t.Errorf("selected row = %q, want counts flush right", sel)
	}
	if !strings.Contains(other, "…") || !strings.Contains(other, "+1 -1") {
		t.Errorf("unselected row = %q, want a clipped name and its counts", other)
	}
}

func TestSelectedTreeNameMarqueeKeepsCounts(t *testing.T) {
	const path = "a_really_quite_long_file_name_indeed.go"
	m := newTestModel(t, treeDiff(path))
	m.sideWidth = SidebarWidthMin
	frame0 := ansi.Cut(screen(t, m, 120, 5)[0], 0, SidebarWidthMin)
	m.marqueeFrames["file:"+path] = marqueeHold + 8
	side := ansi.Cut(screen(t, m, 120, 5)[0], 0, SidebarWidthMin)
	if !strings.HasSuffix(strings.TrimRight(side, " "), "+1 -1") {
		t.Errorf("scrolled selected row = %q, want counts flush right", side)
	}
	if ansi.StringWidth(side) != SidebarWidthMin {
		t.Errorf("scrolled selected row width %d, want %d", ansi.StringWidth(side), SidebarWidthMin)
	}
	if strings.Contains(side, "…") {
		t.Errorf("scrolled selected row = %q, want a window not an ellipsis", side)
	}
	if side == frame0 {
		t.Errorf("scrolled selected row still %q; mid-scroll should move the name", side)
	}
}

func TestClipOrMarqueeKeepsIndependentFrames(t *testing.T) {
	m := &Model{}
	const w = 8
	a := "abcdefghijklmnopqrstuvwxyz0123456789ABCD"
	b := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcd"
	m.clipOrMarquee("commit:1", a, w, true)
	m.marqueeFrames["commit:1"] = 19
	gotA := m.clipOrMarquee("commit:1", a, w, true)
	gotB := m.clipOrMarquee("file:2", b, w, true)
	if m.marqueeFrames["commit:1"] != 19 {
		t.Fatalf("commit frame reset to %d after drawing the file", m.marqueeFrames["commit:1"])
	}
	if gotB != marquee(b, w, 0) {
		t.Fatalf("new file started at %q, want frame 0", gotB)
	}
	if gotA != marquee(a, w, 19) {
		t.Fatalf("commit window %q, want frame 19", gotA)
	}
	for range 20 {
		m.clipOrMarquee("commit:1", a, w, true)
		m.clipOrMarquee("file:2", b, w, true)
		m.marqueeFrames["commit:1"]++
		m.marqueeFrames["file:2"]++
	}
	winA := m.clipOrMarquee("commit:1", a, w, true)
	winB := m.clipOrMarquee("file:2", b, w, true)
	if winA == marquee(a, w, 0) {
		t.Fatal("commit still at frame 0 after interleaved ticks")
	}
	if winB == marquee(b, w, 0) {
		t.Fatal("file still at frame 0 after interleaved ticks")
	}
}

func TestStartMarqueeSkipsWhenNothingOverflows(t *testing.T) {
	m := newTestModel(t, treeDiff("a.go"))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})
	m = updated.(*Model)
	m.marqueeArmed = false
	if cmd := m.startMarquee(); cmd != nil {
		t.Fatal("ticked a sidebar whose names all fit")
	}
}

func TestStartMarqueeArmsWhenSelectedNameOverflows(t *testing.T) {
	m := newTestModel(t, treeDiff("a_really_quite_long_file_name_indeed.go"))
	m.sideWidth = SidebarWidthMin
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})
	m = updated.(*Model)
	m.marqueeArmed = false
	if cmd := m.startMarquee(); cmd == nil {
		t.Fatal("did not tick a truncated selected name")
	}
}

func TestFileKeysFollowTheTree(t *testing.T) {
	m := newTestModel(t, treeDiff(treePaths...))
	screen(t, m, 120, 20)

	var brackets, tree []string
	for range m.files {
		brackets = append(brackets, m.files[m.currentFile()].Path())
		m.command("]")
	}
	m.moveTo(0)
	m.command("ctrl+w")
	// File 0 may sit below directory rows, so walk to the top first. j also
	// stops on directories; those leave the current file alone.
	for range buildTree(m.files, nil) {
		m.command("k")
	}
	for range buildTree(m.files, nil) {
		if m.treeDir == "" {
			tree = append(tree, m.files[m.currentFile()].Path())
		}
		m.command("j")
	}
	var drawn []string
	for _, l := range buildTree(m.files, nil) {
		if l.file >= 0 {
			drawn = append(drawn, m.files[l.file].Path())
		}
	}
	if !slices.Equal(brackets, drawn) || !slices.Equal(tree, drawn) {
		t.Errorf("] walks %v, j walks %v, sidebar draws %v", brackets, tree, drawn)
	}

	// k walks back, clamping at the first file.
	for range len(m.files) + 2 {
		m.command("k")
	}
	if m.currentFile() != 0 {
		t.Errorf("k past the top left file %d, want 0", m.currentFile())
	}
}

func TestClickOnDirectoryDoesNothing(t *testing.T) {
	m := newTestModel(t, treeDiff(treePaths...))
	screen(t, m, 120, 20)
	m.moveTo(m.view.FileRows[3])
	cur := m.cur

	m = clickAt(t, m, 3, 0) // a/
	if m.cur != cur || m.focus != focusDiff {
		t.Errorf("click on a directory moved cursor %d -> %d, focus %v", cur, m.cur, m.focus)
	}

	m = clickAt(t, m, 3, 2) // c.go
	if m.currentFile() != 0 || m.focus != focusTree {
		t.Errorf("click on c.go: file %d focus %v, want file 0 and the tree", m.currentFile(), m.focus)
	}
	m = clickAt(t, m, sidebarWidth+4, 2)
	if m.focus != focusDiff {
		t.Errorf("click in the body left focus on %v", m.focus)
	}
}

func TestCtrlWCyclesPanels(t *testing.T) {
	m := newTestModel(t, sample)
	screen(t, m, 120, 20)

	m.handleKey(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if m.panelFocus() != focusTree {
		t.Fatalf("ctrl+w from the diff focused %v, want the tree", m.panelFocus())
	}
	// The tree's rule lights up.
	if !strings.Contains(m.render(), m.st.focusRail.Render("│")) {
		t.Error("focused tree does not light its rule")
	}
	cur := m.cur
	m.command("j")
	if m.currentFile() != 1 {
		t.Errorf("j in the tree went to file %d, want 1", m.currentFile())
	}
	if m.cur == cur+1 {
		t.Error("j in the tree scrolled the diff a line")
	}

	m.command("ctrl+w")
	if m.panelFocus() != focusDiff {
		t.Fatalf("ctrl+w from the tree focused %v, want the diff", m.panelFocus())
	}

	// Hiding the sidebar hands focus back to the diff, and ctrl+w has nowhere
	// else to go.
	m.command("ctrl+w")
	m.command("b")
	if m.panelFocus() != focusDiff {
		t.Errorf("b left focus on %v", m.panelFocus())
	}
	m.command("ctrl+w")
	if m.panelFocus() != focusDiff {
		t.Errorf("ctrl+w with no sidebar focused %v", m.panelFocus())
	}
}

func TestLogCtrlWCyclesThreePanels(t *testing.T) {
	m, _ := logModel(t)
	screen(t, m, 140, 24)

	m.command("ctrl+w")
	if m.panelFocus() != focusCommits {
		t.Fatalf("first ctrl+w focused %v, want commits", m.panelFocus())
	}
	if out := m.render(); !strings.Contains(out, m.sidebarHeader("Commits", m.sidebarW(), true)) {
		t.Error("the focused Commits header is not highlighted")
	}
	m.command("j")
	if m.commitIdx != 1 {
		t.Errorf("j on commits left commitIdx %d, want 1", m.commitIdx)
	}
	m.command("k")
	if m.commitIdx != 0 {
		t.Errorf("k on commits left commitIdx %d, want 0", m.commitIdx)
	}

	m.command("ctrl+w")
	if m.panelFocus() != focusTree {
		t.Fatalf("second ctrl+w focused %v, want files", m.panelFocus())
	}
	m.command("j")
	if m.commitIdx != 0 {
		t.Errorf("j on files changed commit to %d", m.commitIdx)
	}

	m.command("ctrl+w")
	if m.panelFocus() != focusDiff {
		t.Fatalf("third ctrl+w focused %v, want the diff", m.panelFocus())
	}
}

func TestLogCtrlWSkipsFilesOfAnEmptyCommit(t *testing.T) {
	m, _ := logModel(t)
	screen(t, m, 140, 24)
	m.files = nil
	m.rebuildView()

	m.command("ctrl+w")
	m.command("ctrl+w")
	if m.panelFocus() != focusDiff {
		t.Errorf("ctrl+w twice with no files focused %v, want back to the diff", m.panelFocus())
	}
}

func TestShiftArrowsResizeTheSidebar(t *testing.T) {
	m := newTestModel(t, sample)
	screen(t, m, 200, 20)

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	if got := m.sidebarW(); got != sidebarWidth+sidebarStep {
		t.Errorf("shift+right: width %d, want %d", got, sidebarWidth+sidebarStep)
	}
	for range 20 {
		m.command("shift+right")
	}
	if got := m.sidebarW(); got != SidebarWidthMax {
		t.Errorf("widened past the max: %d", got)
	}
	for range 20 {
		m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
	}
	if got := m.sidebarW(); got != SidebarWidthMin {
		t.Errorf("narrowed past the min: %d", got)
	}

	// The diff always keeps its minimum.
	m.sideWidth = SidebarWidthMax
	screen(t, m, 100, 20)
	if got := m.contentWidth(); got < minDiffWidth {
		t.Errorf("diff got %d columns, want at least %d", got, minDiffWidth)
	}
}

func TestSidebarWidthOption(t *testing.T) {
	files, err := diff.ParseString(sample)
	if err != nil {
		t.Fatal(err)
	}
	m := New(files, theme.Default(), Options{SidebarWidth: 40})
	screen(t, m, 200, 20)
	if got := m.sidebarW(); got != 40 {
		t.Errorf("--sidebar-width 40 opened %d wide", got)
	}
}

func TestResizeFallsBackToUnifiedAndKeepsTheCursor(t *testing.T) {
	m := newTestModel(t, sample)
	screen(t, m, 115, 20)
	if !m.builtSplit {
		t.Fatal("expected split at this width")
	}
	m.moveTo(m.view.HunkRows[0] + 2)
	row := m.view.Rows[m.cur]

	m.command("shift+right")
	m.command("shift+right")
	if m.builtSplit {
		t.Fatalf("sidebar %d still leaves room for split", m.sidebarW())
	}
	got := m.view.Rows[m.cur]
	if got.FileIdx != row.FileIdx || got.HunkIdx != row.HunkIdx || m.cur == m.view.HunkRows[0] {
		t.Errorf("cursor moved from file %d hunk %d to file %d hunk %d row %d",
			row.FileIdx, row.HunkIdx, got.FileIdx, got.HunkIdx, m.cur)
	}
}

func TestDirectoryTurnsGreenWhenEverythingUnderItIsApproved(t *testing.T) {
	m := newTestModel(t, treeDiff(treePaths...))
	screen(t, m, 120, 20)
	m.marks = marks{}
	tree := buildTree(m.files, nil)
	green := func(i int) bool {
		l := tree[i]
		want := m.st.sidebar.Foreground(m.st.stagedFg).Render(l.prefix[len(l.prefix)-len("├─"):] + l.name + "/")
		return strings.Contains(m.treeRow(tree, i, sidebarWidth, false), want)
	}

	if green(0) || green(1) {
		t.Fatal("directories are green with nothing marked")
	}
	m.marks.set(0, 0, true) // a/b/c.go
	if !green(1) {
		t.Error("a/b/ is not green with its only file marked")
	}
	if green(0) {
		t.Error("a/ is green with a/z.go still unmarked")
	}
	m.marks.set(1, 0, true) // a/z.go
	if !green(0) {
		t.Error("a/ is not green once every file under it is marked")
	}
}

func TestPartialMarksDoNotApprove(t *testing.T) {
	m := newTestModel(t, twoHunkDiff)
	m.marks = marks{}
	m.marks.set(0, 0, true)
	if m.approved(0) {
		t.Error("one of two hunks marked counts as approved")
	}
	m.marks.set(0, 1, true)
	if !m.approved(0) {
		t.Error("every hunk marked does not count as approved")
	}
}

func TestStagedFilesAreApproved(t *testing.T) {
	base := lines(60)
	edited := replaceLine(base, 5, "FIRST")
	edited = replaceLine(edited, 30, "SECOND")
	m, repo := gitModel(t,
		map[string]string{"a.txt": base, "b.txt": base},
		map[string]string{"a.txt": edited, "b.txt": replaceLine(base, 3, "CHANGED")},
	)
	before := map[string][]byte{}
	for _, name := range []string{"a.txt", "b.txt"} {
		b, err := os.ReadFile(filepath.Join(repo.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = b
	}

	// Stage b.txt whole, and the first hunk of a.txt.
	m.moveTo(m.view.FileRows[1])
	m.command("a")
	m.moveTo(m.view.HunkRows[0])
	m.command("space")
	m.command("w")

	if !m.approved(1) {
		t.Error("fully staged b.txt is not approved")
	}
	if m.approved(0) || m.allApproved(0, 2) {
		t.Error("a.txt, half staged, counts as approved")
	}
	m.marks.set(0, 0, true) // the hunk a.txt has left
	if !m.approved(0) || !m.allApproved(0, 2) {
		t.Error("staged plus the rest marked is not approved")
	}

	for name, b := range before {
		after, err := os.ReadFile(filepath.Join(repo.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(b) {
			t.Errorf("staging changed %s in the working tree", name)
		}
	}
}

func TestFoldingADirectory(t *testing.T) {
	m := newTestModel(t, treeDiff(treePaths...))
	screen(t, m, 120, 20)
	m.command("ctrl+w")
	// Tree starts on a/b/c.go; k twice lands on a/.
	m.command("k") // c.go -> b/
	m.command("k") // b/ -> a/
	if m.treeDir != "a" || m.currentFile() != 0 {
		t.Fatalf("k onto a/ selected %q with file %d, want a with a/b/c.go still current", m.treeDir, m.currentFile())
	}
	names := func() (out []string) {
		for _, l := range buildTree(m.files, m.collapsed) {
			out = append(out, l.name)
		}
		return out
	}

	// space folds it: its line stays, everything under it goes.
	m.command("space")
	if want := []string{"a", "README", "a.go", "b.go"}; !slices.Equal(names(), want) {
		t.Fatalf("space on a/ drew %v, want %v", names(), want)
	}
	if out := strings.Join(screen(t, m, 120, 20), "\n"); !strings.Contains(ansi.Strip(out), "├+a/") {
		t.Errorf("folded a/ has no +:\n%s", out)
	}
	m.command("j")
	if m.files[m.currentFile()].Path() != "README" {
		t.Errorf("j past folded a/ landed on %s, want README", m.files[m.currentFile()].Path())
	}
	m.command("k")
	m.command("+")
	if len(names()) != 7 {
		t.Errorf("+ on a/ drew %v, want everything open", names())
	}
	m.command("-")
	if len(names()) != 4 {
		t.Errorf("- on a/ drew %v, want a/ folded", names())
	}

	// [ leaves the directory for a file and does not unfold it; a file hidden
	// inside the fold is shown by highlighting the fold.
	m.command("j") // README
	m.command("[") // from README to a/z.go, hidden
	if m.treeDir != "" || m.files[m.currentFile()].Path() != "a/z.go" {
		t.Errorf("[ from README gave dir %q file %s", m.treeDir, m.files[m.currentFile()].Path())
	}
	if tree := buildTree(m.files, m.collapsed); tree[m.treeSel(tree)].name != "a" {
		t.Errorf("hidden a/z.go highlights %s, want a/", tree[m.treeSel(tree)].name)
	}

	// With the cursor on a file, - is the context key again, not a fold.
	m.command("ctrl+w")
	m.command("-")
	if len(names()) != 4 {
		t.Errorf("- in the diff changed the tree: %v", names())
	}
}

func TestSelectedNameOverflowsAgreesWithPaint(t *testing.T) {
	long := newTestModel(t, treeDiff("a_really_quite_long_file_name_indeed.go"))
	long.sideWidth = SidebarWidthMin
	screen(t, long, 120, 10)
	if !long.marqueeOverflow {
		t.Fatal("paint did not record an overflowing selected name")
	}
	if !long.selectedNameOverflows() {
		t.Fatal("compute disagrees with paint on an overflowing name")
	}

	short := newTestModel(t, treeDiff("a.go"))
	screen(t, short, 120, 10)
	if short.marqueeOverflow || short.selectedNameOverflows() {
		t.Fatal("a name that fits should not overflow")
	}
}

func TestMarqueeTickReusesPaintOverflow(t *testing.T) {
	m := newTestModel(t, treeDiff("a_really_quite_long_file_name_indeed.go"))
	m.sideWidth = SidebarWidthMin
	screen(t, m, 120, 10)
	m.files = nil // compute would now say nothing overflows
	m.marqueeArmed = false
	if cmd := m.startMarqueeFromPaint(); cmd == nil {
		t.Fatal("tick path required a tree rebuild")
	}
	m.marqueeArmed = false
	if cmd := m.startMarquee(); cmd != nil {
		t.Fatal("compute path should see no files")
	}
}

func TestCommitSubjectWidthSharedWithCommitLine(t *testing.T) {
	m, _ := logModel(t)
	m.sideWidth = SidebarWidthMin
	screen(t, m, 120, 16)
	c := m.commit()
	w := m.sidebarW()
	room := commitSubjectWidth(w, c.Short)
	if room != w-ansi.StringWidth(c.Short)-3 {
		t.Fatalf("commitSubjectWidth = %d, want %d", room, w-ansi.StringWidth(c.Short)-3)
	}
}
