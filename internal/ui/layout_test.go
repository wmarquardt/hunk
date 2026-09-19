package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/wmarquardt/hunk/internal/diff"
)

// pairs renders a hunk's paired rows as "left|right" strings, which is close
// enough to what the screen shows to assert on directly.
func pairs(t *testing.T, unified string) []string {
	t.Helper()
	files, err := diff.ParseString(unified)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || len(files[0].Hunks) != 1 {
		t.Fatalf("fixture should hold exactly one hunk, got %d files", len(files))
	}

	var out []string
	for _, r := range hunkRows(files[0].Hunks[0], true) {
		out = append(out, r.Left.Text+"|"+r.Right.Text)
	}
	return out
}

func TestHunkRowsPairing(t *testing.T) {
	tests := []struct {
		name string
		diff string
		want []string
	}{
		{
			name: "context lines sit on both sides",
			diff: "--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n one\n two\n-x\n+y\n",
			want: []string{"one|one", "two|two", "x|y"},
		},
		{
			name: "a removal and an addition pair up",
			diff: "--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n one\n-two\n+2\n",
			want: []string{"one|one", "two|2"},
		},
		{
			name: "an unbalanced run pads the short side",
			diff: "--- a/x\n+++ b/x\n@@ -1,1 +1,3 @@\n-a\n+1\n+2\n+3\n",
			want: []string{"a|1", "|2", "|3"},
		},
		{
			name: "a pure deletion leaves the right side empty",
			diff: "--- a/x\n+++ b/x\n@@ -1,3 +1,1 @@\n keep\n-gone\n-also gone\n",
			want: []string{"keep|keep", "gone|", "also gone|"},
		},
		{
			name: "a second removal run starts a new pairing",
			diff: "--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n-a\n+1\n-b\n+2\n",
			want: []string{"a|1", "b|2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pairs(t, tt.diff)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d rows %q, want %d %q", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("row %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestHunkRowsLineNumbers(t *testing.T) {
	files, err := diff.ParseString("--- a/x\n+++ b/x\n@@ -10,2 +20,2 @@\n keep\n-old\n+new\n")
	if err != nil {
		t.Fatal(err)
	}
	rows := hunkRows(files[0].Hunks[0], true)

	if rows[0].Left.Num != 10 || rows[0].Right.Num != 20 {
		t.Errorf("context row numbers = %d/%d, want 10/20", rows[0].Left.Num, rows[0].Right.Num)
	}
	if rows[1].Left.Num != 11 || rows[1].Right.Num != 21 {
		t.Errorf("changed row numbers = %d/%d, want 11/21", rows[1].Left.Num, rows[1].Right.Num)
	}
}

func TestHunkRowsEmptySideHasNoLineNumber(t *testing.T) {
	files, err := diff.ParseString("--- a/x\n+++ b/x\n@@ -1,1 +1,2 @@\n keep\n+added\n")
	if err != nil {
		t.Fatal(err)
	}
	rows := hunkRows(files[0].Hunks[0], true)

	added := rows[len(rows)-1]
	if !added.Left.Empty {
		t.Error("left side of a pure addition should be empty")
	}
	if added.Left.Num != 0 {
		t.Errorf("empty side has line number %d, want 0", added.Left.Num)
	}
}

func TestBuildIndexes(t *testing.T) {
	unified := "" +
		"--- a/one.txt\n+++ b/one.txt\n@@ -1,1 +1,1 @@\n-a\n+b\n" +
		"--- a/two.txt\n+++ b/two.txt\n@@ -1,1 +1,1 @@\n-c\n+d\n@@ -9,1 +9,1 @@\n-e\n+f\n"

	files, err := diff.ParseString(unified)
	if err != nil {
		t.Fatal(err)
	}
	v := Build(files, true)

	if len(v.FileRows) != 2 {
		t.Fatalf("FileRows = %v, want 2 entries", v.FileRows)
	}
	if len(v.HunkRows) != 3 {
		t.Fatalf("HunkRows = %v, want 3 entries", v.HunkRows)
	}
	for _, i := range v.FileRows {
		if v.Rows[i].Kind != RowFile {
			t.Errorf("row %d is not a file header", i)
		}
	}
	for _, i := range v.HunkRows {
		if v.Rows[i].Kind != RowHunk {
			t.Errorf("row %d is not a hunk header", i)
		}
	}
	// Indexes must be ascending, or next/prev navigation walks backwards.
	for i := 1; i < len(v.HunkRows); i++ {
		if v.HunkRows[i] <= v.HunkRows[i-1] {
			t.Errorf("HunkRows not ascending: %v", v.HunkRows)
		}
	}
}

func TestBuildBinaryAndEmptyFiles(t *testing.T) {
	files, err := diff.ParseString(
		"diff --git a/x.bin b/x.bin\n--- a/x.bin\n+++ b/x.bin\nBinary files a/x.bin and b/x.bin differ\n")
	if err != nil {
		t.Fatal(err)
	}
	v := Build(files, true)

	if len(v.HunkRows) != 0 {
		t.Errorf("a binary file should contribute no hunks, got %v", v.HunkRows)
	}
	var notice bool
	for _, r := range v.Rows {
		if r.Kind == RowNotice {
			notice = true
		}
	}
	if !notice {
		t.Error("a binary file should show a notice row rather than nothing")
	}
}

func TestBuildEmptyInput(t *testing.T) {
	v := Build(nil, true)
	if len(v.Rows) != 0 || len(v.FileRows) != 0 || len(v.HunkRows) != 0 {
		t.Errorf("empty input produced %+v", v)
	}
}

func TestNextPrevIndex(t *testing.T) {
	idx := []int{5, 10, 20}

	tests := []struct {
		name       string
		cur        int
		next, prev int
	}{
		{name: "before the first", cur: 0, next: 5, prev: 5},
		{name: "on the first", cur: 5, next: 10, prev: 5},
		{name: "between", cur: 7, next: 10, prev: 5},
		{name: "on the last", cur: 20, next: 20, prev: 10},
		{name: "past the last", cur: 99, next: 20, prev: 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextIndex(idx, tt.cur); got != tt.next {
				t.Errorf("NextIndex(%d) = %d, want %d", tt.cur, got, tt.next)
			}
			if got := PrevIndex(idx, tt.cur); got != tt.prev {
				t.Errorf("PrevIndex(%d) = %d, want %d", tt.cur, got, tt.prev)
			}
		})
	}
}

func TestNextPrevIndexEmpty(t *testing.T) {
	if got := NextIndex(nil, 3); got != 3 {
		t.Errorf("NextIndex on an empty index = %d, want the cursor unmoved", got)
	}
	if got := PrevIndex(nil, 3); got != 3 {
		t.Errorf("PrevIndex on an empty index = %d, want the cursor unmoved", got)
	}
}

// boxSpans reports, per pane, the rows a change block's outline covers: the
// index of its opening rule and of its closing rule. Each pane closes on its
// own last changed line, which is the whole point of the shape.
func boxSpans(t *testing.T, unified string) (left, right [][2]int, arrows int) {
	t.Helper()
	files, err := diff.ParseString(unified)
	if err != nil {
		t.Fatal(err)
	}
	openLeft, openRight := -1, -1
	for i, r := range Build(files, true).Rows {
		if r.Arrow {
			arrows++
		}
		switch r.BoxLeft {
		case BoxTop:
			openLeft = i
		case BoxBottom:
			left = append(left, [2]int{openLeft, i})
		}
		switch r.BoxRight {
		case BoxTop:
			openRight = i
		case BoxBottom:
			right = append(right, [2]int{openRight, i})
		}
	}
	return left, right, arrows
}

func TestWrapBlocksOutlinesEachPaneOnItsOwnLines(t *testing.T) {
	tests := []struct {
		name        string
		diff        string
		left, right [][2]int
		arrows      int
	}{
		{
			name:   "a one-for-one change closes both panes together",
			diff:   "--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n one\n-two\n+2\n",
			left:   [][2]int{{3, 5}},
			right:  [][2]int{{3, 5}},
			arrows: 1,
		},
		{
			name:   "one line becoming three closes the left pane first",
			diff:   "--- a/x\n+++ b/x\n@@ -1,1 +1,3 @@\n-a\n+1\n+2\n+3\n",
			left:   [][2]int{{2, 4}}, // opens above the run, shuts after its one line
			right:  [][2]int{{2, 6}}, // runs on to the last addition
			arrows: 1,
		},
		{
			name:   "context between two runs makes two blocks",
			diff:   "--- a/x\n+++ b/x\n@@ -1,4 +1,4 @@\n-a\n+1\n keep\n-b\n+2\n still\n",
			left:   [][2]int{{2, 4}, {6, 8}},
			right:  [][2]int{{2, 4}, {6, 8}},
			arrows: 2,
		},
		{
			name:   "a hole on one side does not split its box",
			diff:   "--- a/x\n+++ b/x\n@@ -1,2 +1,3 @@\n-a\n+1\n+2\n-b\n+3\n",
			left:   [][2]int{{2, 6}},
			right:  [][2]int{{2, 6}},
			arrows: 1,
		},
		{
			name:   "a pure addition boxes only the right pane, with no arrow",
			diff:   "--- a/x\n+++ b/x\n@@ -1,1 +1,2 @@\n keep\n+added\n",
			left:   nil,
			right:  [][2]int{{3, 5}},
			arrows: 0,
		},
		{
			name:   "a pure deletion boxes only the left pane, with no arrow",
			diff:   "--- a/x\n+++ b/x\n@@ -1,2 +1,1 @@\n keep\n-gone\n",
			left:   [][2]int{{3, 5}},
			right:  nil,
			arrows: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left, right, arrows := boxSpans(t, tt.diff)
			if !equalSpans(left, tt.left) {
				t.Errorf("left pane boxes = %v, want %v", left, tt.left)
			}
			if !equalSpans(right, tt.right) {
				t.Errorf("right pane boxes = %v, want %v", right, tt.right)
			}
			if arrows != tt.arrows {
				t.Errorf("got %d direction markers, want %d", arrows, tt.arrows)
			}
		})
	}
}

func equalSpans(got, want [][2]int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Unified view has one column, so one box wraps the whole run however lopsided
// the change is.
func TestWrapBlocksUnifiedKeepsOneBox(t *testing.T) {
	files, err := diff.ParseString("--- a/x\n+++ b/x\n@@ -1,1 +1,3 @@\n-a\n+1\n+2\n+3\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range Build(files, false).Rows {
		if r.BoxLeft != r.BoxRight {
			t.Fatalf("unified row %q has BoxLeft=%v BoxRight=%v, want them equal",
				r.Left.Text+r.Right.Text, r.BoxLeft, r.BoxRight)
		}
		if r.Arrow {
			t.Error("unified view has no second pane to point at")
		}
	}
}

func TestWrapBlocksMarksEnclosedRows(t *testing.T) {
	files, err := diff.ParseString("--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n keep\n-old\n+new\n last\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range Build(files, true).Rows {
		if r.Kind != RowPair {
			continue
		}
		changed := r.Left.Kind == diff.Removed || r.Right.Kind == diff.Added
		if boxed := r.BoxLeft == BoxMid; boxed != changed {
			t.Errorf("row %q/%q is enclosed=%v, want %v", r.Left.Text, r.Right.Text, boxed, changed)
		}
	}
}

func TestMarquee(t *testing.T) {
	const s = "abcdefghij" // 10 columns
	const w = 4
	maxOff := ansi.StringWidth(s) - w // 6
	cycle := 2*marqueeHold + maxOff

	if got := marquee("abc", 8, 99); got != "abc" {
		t.Errorf("fitting name = %q, want unchanged on every frame", got)
	}
	start := marquee(s, w, 0)
	if start != "abcd" {
		t.Fatalf("frame 0 = %q, want abcd", start)
	}
	for i := 1; i < marqueeHold; i++ {
		if got := marquee(s, w, i); got != start {
			t.Fatalf("hold-start frame %d = %q, want %q", i, got, start)
		}
	}
	lastScroll := marqueeHold + maxOff - 1
	end := marquee(s, w, lastScroll)
	if end != "ghij" {
		t.Fatalf("last scroll frame = %q, want ghij", end)
	}
	if ansi.StringWidth(end) != w {
		t.Fatalf("last scroll width %d, want %d", ansi.StringWidth(end), w)
	}
	for i := lastScroll + 1; i < cycle; i++ {
		if got := marquee(s, w, i); got != end {
			t.Fatalf("hold-end frame %d = %q, want %q", i, got, end)
		}
	}
	if got := marquee(s, w, cycle); got != start {
		t.Fatalf("wrap frame %d = %q, want %q", cycle, got, start)
	}

	wide := "日本語ファイル"
	win := marquee(wide, 4, 0)
	if ansi.StringWidth(win) > 4 {
		t.Fatalf("CJK frame 0 width %d > 4: %q", ansi.StringWidth(win), win)
	}
	last := marquee(wide, 4, marqueeHold+ansi.StringWidth(wide)-4-1)
	if ansi.StringWidth(last) > 4 {
		t.Fatalf("CJK last-scroll width %d > 4: %q", ansi.StringWidth(last), last)
	}
	// The last scroll window is the tail: cutting from maxOff must not clip
	// the end of the name.
	if got := ansi.StringWidth(wide) - ansi.StringWidth(last); got < 0 {
		t.Fatalf("CJK last-scroll %q is wider than the name", last)
	}
}
