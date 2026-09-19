package ui

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wmarquardt/hunk/internal/diff"
	"github.com/wmarquardt/hunk/internal/git"
	"github.com/wmarquardt/hunk/internal/theme"
)

// markRepeat is how long after marking one hunk a space press on a *different*
// hunk is read as the key repeating rather than as a second decision. Marking
// moves the cursor on, so without this a held space bar walks the file and
// marks all of it. Pressing space again on the same hunk is never blocked, so
// taking a mark straight back off still works.
const markRepeat = time.Second

const (
	sidebarWidth = 28
	// logSidebarWidth is the sidebar in history mode, where it carries a short
	// sha and a subject as well as the file list.
	logSidebarWidth = 36
	// sidebarStep is how far shift+← / shift+→ move the sidebar's edge.
	sidebarStep = 4
	// minDiffWidth is how much room a wide sidebar must always leave the diff.
	minDiffWidth = 60
	// minSidebarWidth is the total terminal width below which the sidebar is
	// hidden: past this point it costs more than it tells you.
	minSidebarWidth = 100
	// minSplitWidth is the content width below which side-by-side collapses to
	// a unified view rather than showing two unreadable columns.
	minSplitWidth = 80
	// logAuthorWidth is the terminal width at which history mode's status bar
	// has room for who wrote the commit as well as the key hints.
	logAuthorWidth = 160
	numWidth       = 5
)

// SidebarWidthMin and SidebarWidthMax bound the sidebar width a user can pick
// with --sidebar-width or shift+← / shift+→.
const (
	SidebarWidthMin = 20
	SidebarWidthMax = 48
)

// Model is the whole TUI state.
type Model struct {
	files []diff.File
	view  *View
	st    styles

	width, height int

	cur     int // cursor row, the row navigation acts on
	top     int // first visible row
	hscroll int

	// wantSplit and wantSidebar are what the user asked for; the terminal's
	// width decides what they actually get.
	wantSplit   bool
	wantSidebar bool
	builtSplit  bool

	// sideWidth is the sidebar width the user picked; 0 means the mode's default.
	sideWidth int

	// focus is the panel ↑ / ↓ and j / k act on. ctrl+w cycles it.
	focus panel

	// collapsed holds the directory paths folded shut in the tree. treeDir is the
	// directory the tree cursor rests on, or "" when it is on the current file;
	// it only counts while the tree has focus.
	collapsed map[string]bool
	treeDir   string

	showHelp bool

	// Git review mode. repo is nil for a plain diff, in which case marking and
	// staging are not offered at all.
	repo  *git.Repo
	marks marks
	msg   string

	// undoStack records each stage so "u" can reverse them one at a time,
	// most recent first. A single slot would lose every stage but the last.
	undoStack []stageRecord

	// staged and unstaged are the paths git reports as having index and
	// working-tree changes, so the sidebar can show what is already approved.
	staged   map[string]bool
	unstaged map[string]bool

	// Commit history mode. commits is the log, newest first, and commitIdx is
	// the commit on screen. History is read: logMode keeps marking and staging
	// off even though repo is set, so nothing here can write the index.
	commits   []git.Commit
	commitIdx int
	logMode   bool

	// Live-follow: watch reports working-tree changes and live is whether hunk
	// currently acts on them. Both are zero for a plain diff, which never
	// follows anything.
	watch *watcher
	live  bool

	// ignoreWS re-diffs with git's -w, hiding whitespace-only changes. Only the
	// git review mode can honour it, since it is the only source hunk can re-run.
	ignoreWS bool

	// Search, vim-style. searchInput is true while the / prompt is open; typed
	// is the query being edited; search is the confirmed query that n / N repeat
	// and esc clears. commitSearch is the same thing for the commits panel, kept
	// apart so a search in one panel does not clobber the other. searchPanel is
	// which panel had focus when / was pressed, so Enter still lands there if a
	// resize during typing changes panelFocus().
	searchInput  bool
	typed        string
	search       string
	commitSearch string
	searchPanel  panel

	// context is how many unchanged lines surround each hunk (git's -U). + and -
	// re-diff with more or less. Git review mode only, since it re-sources.
	context int

	// showWS renders tabs and trailing spaces as visible marks. It is purely a
	// rendering choice — no re-diff — so it works in every mode.
	showWS bool

	// syntax turns on language-aware highlighting; hl does the coloring. Both are
	// a rendering choice, no re-diff.
	syntax bool
	hl     *highlighter

	// raw is every parsed file; files is raw after the regex filter drops hunks
	// whose every changed line matches. filter nil means files == raw.
	raw    []diff.File
	filter *regexp.Regexp

	// Regex filter prompt (F), same shape as search.
	filterInput bool
	filterTyped string
	filterSrc   string

	// clock, lastMark and lastMarkAt tell a held space bar from a deliberate
	// second press. clock is a field so tests do not have to sleep.
	clock      func() time.Time
	lastMark   [2]int
	lastMarkAt time.Time

	// toastText floats in a box centered on screen until toastUntil.
	toastText  string
	toastUntil time.Time

	// marqueeFrames is the scroll clock for each selected sidebar row that is
	// currently on screen. Log mode draws a commit and a file in the same
	// frame; one slot would reset the other every call. marqueeSeen is the
	// keys clipOrMarquee touched this render, so rows that left the selection
	// snap back to the start next time. marqueeArmed is whether a tick is
	// already scheduled. marqueeOverflow is whether the last sidebar paint
	// drew a selected name that did not fit — marquee ticks reuse that so
	// they do not rebuild the tree just to answer the same boolean.
	marqueeFrames   map[string]int
	marqueeSeen     map[string]bool
	marqueeArmed    bool
	marqueeOverflow bool

	// hints are the clickable option zones on the status bar, rebuilt on every
	// render so mouse hit-testing matches exactly what is on screen.
	hints []hintZone
}

// panel is one of the screen's focusable areas.
type panel int

const (
	focusDiff panel = iota
	focusCommits
	focusTree
)

// hintZone maps a horizontal span of the status bar to the key its label
// stands for, so a click on the label does what the key does.
type hintZone struct {
	x0, x1 int // inclusive column range on the status row
	key    string
}

// Options are the startup preferences the command-line flags set. Every one
// has an in-app key that still toggles it during a session; these just pick the
// state hunk opens in.
type Options struct {
	IgnoreWS  bool   // -w: open with whitespace-only changes hidden
	Unified   bool   // -u: open unified instead of side-by-side
	NoSidebar bool   // --no-sidebar: open with the file sidebar hidden
	NoFollow  bool   // --no-follow: open with live-follow paused
	Context   int    // --context: unchanged lines around each hunk (0 falls back to the default)
	ShowWS    bool   // --show-whitespace: render tabs and trailing spaces as marks
	Filter    string // --filter: hide hunks whose every changed line matches this regex
	NoSyntax  bool   // --no-syntax: open with syntax highlighting off
	// SidebarWidth is --sidebar-width: the sidebar's columns, 0 for the default.
	SidebarWidth int
}

// New builds a model over an already-parsed diff.
func New(files []diff.File, t *theme.Theme, opts Options) *Model {
	context := opts.Context
	if context <= 0 {
		context = diff.DefaultContext
	}
	m := &Model{
		raw:         files,
		st:          newStyles(t),
		wantSplit:   !opts.Unified,
		wantSidebar: !opts.NoSidebar,
		sideWidth:   opts.SidebarWidth,
		builtSplit:  !opts.Unified,
		ignoreWS:    opts.IgnoreWS,
		context:     context,
		showWS:      opts.ShowWS,
		syntax:      !opts.NoSyntax,
		hl:          newHighlighter(t.Syntax),
		clock:       time.Now,
		lastMark:    [2]int{-1, -1},
	}
	if opts.Filter != "" {
		// A bad pattern from the flag is main's job to reject; ignore it here.
		m.filter, m.filterSrc = compileFilter(opts.Filter)
	}
	m.applyFilter()
	m.rebuildView()
	return m
}

// compileFilter returns the compiled regex and the source it came from, or a
// nil regex when the pattern does not compile.
func compileFilter(src string) (*regexp.Regexp, string) {
	re, err := regexp.Compile(src)
	if err != nil {
		return nil, ""
	}
	return re, src
}

// applyFilter recomputes the visible files from raw and the active filter.
// The files are put in tree order first, so the file list, the sidebar and
// ] / [ all agree on what comes next.
func (m *Model) applyFilter() {
	sortFiles(m.raw)
	m.files = filterFiles(m.raw, m.filter)
}

// filterFiles drops every hunk whose changed lines all match re, and every file
// left with no hunks. A nil re shows everything. Binary/hunkless files are kept
// as-is: a line filter has nothing to say about them.
func filterFiles(files []diff.File, re *regexp.Regexp) []diff.File {
	if re == nil {
		return files
	}
	out := make([]diff.File, 0, len(files))
	for _, f := range files {
		if len(f.Hunks) == 0 {
			out = append(out, f)
			continue
		}
		kept := make([]diff.Hunk, 0, len(f.Hunks))
		var added, removed int
		for _, h := range f.Hunks {
			if hunkAllMatch(h, re) {
				continue
			}
			kept = append(kept, h)
			for _, l := range h.Lines {
				switch l.Kind {
				case diff.Added:
					added++
				case diff.Removed:
					removed++
				}
			}
		}
		if len(kept) == 0 {
			continue
		}
		f.Hunks, f.Added, f.Removed = kept, added, removed
		out = append(out, f)
	}
	return out
}

// hunkAllMatch reports whether every changed (added/removed) line in the hunk
// matches re — i.e. the hunk is pure noise the filter should hide.
func hunkAllMatch(h diff.Hunk, re *regexp.Regexp) bool {
	changed := 0
	for _, l := range h.Lines {
		if l.Kind == diff.Context {
			continue
		}
		changed++
		if !re.MatchString(l.Text) {
			return false
		}
	}
	return changed > 0
}

// rebuildView re-flattens the files into rows. Everything that changes the diff
// or the layout goes through here.
func (m *Model) rebuildView() {
	m.view = Build(m.files, m.builtSplit)
}

// NewGit is New for a working tree hunk can stage into. It also starts
// following the working tree, so edits made while hunk is open show up on their
// own. A watcher that fails to start just leaves live-follow off.
func NewGit(repo *git.Repo, files []diff.File, t *theme.Theme, opts Options) *Model {
	m := New(files, t, opts)
	m.repo = repo
	m.marks = marks{}
	m.refreshGitState()

	dir := repo.Dir
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	// The watcher still starts when following is off, so f can resume it later.
	if w, err := newWatcher(dir); err == nil {
		m.watch, m.live = w, !opts.NoFollow
	}
	return m
}

// refreshGitState reads which files git considers staged and unstaged, so the
// sidebar can mark approved files without re-deriving it from the diff text.
func (m *Model) refreshGitState() {
	m.staged, m.unstaged = map[string]bool{}, map[string]bool{}
	if m.repo == nil {
		return
	}
	if paths, err := m.repo.StagedPaths(); err == nil {
		for _, p := range paths {
			m.staged[p] = true
		}
	}
	if paths, err := m.repo.UnstagedPaths(); err == nil {
		for _, p := range paths {
			m.unstaged[p] = true
		}
	}
	if paths, err := m.repo.Untracked(); err == nil {
		for _, p := range paths {
			m.unstaged[p] = true
		}
	}
}

// Run puts the model on screen and blocks until the user quits.
func Run(files []diff.File, t *theme.Theme, opts Options) error {
	_, err := tea.NewProgram(New(files, t, opts)).Run()
	return err
}

// RunGit is Run with staging enabled.
func RunGit(repo *git.Repo, files []diff.File, t *theme.Theme, opts Options) error {
	m := NewGit(repo, files, t, opts)
	defer m.watch.Close() // nil-safe; stops the follow goroutine on quit
	_, err := tea.NewProgram(m).Run()
	return err
}

// staging reports whether this session can write to the index. Reading history
// is a repo session that cannot: the past is not something to stage.
func (m *Model) staging() bool { return m.repo != nil && !m.logMode }

// sidebarW is the sidebar width: what the user picked, or this mode's default,
// shrunk if needed so the diff keeps minDiffWidth columns.
func (m *Model) sidebarW() int {
	w := m.sideWidth
	switch {
	case w > 0:
	case m.logMode:
		w = logSidebarWidth
	default:
		w = sidebarWidth
	}
	if m.width > 0 {
		w = min(w, m.width-1-minDiffWidth)
	}
	return max(w, 1)
}

// resizeSidebar widens (dir 1) or narrows (dir -1) the sidebar a step, keeping
// the cursor where it is even if the diff has to switch between split and
// unified to fit.
func (m *Model) resizeSidebar(dir int) {
	m.sideWidth = min(max(m.sidebarW()+dir*sidebarStep, SidebarWidthMin), SidebarWidthMax)
	m.rebuildIfNeeded()
	m.ensureVisible()
	m.msg = fmt.Sprintf("sidebar: %d columns", m.sidebarW())
}

// panelFocus is the panel that has focus right now. A panel that is hidden or
// has nothing in it cannot hold focus, so it falls back to the diff.
func (m *Model) panelFocus() panel {
	if m.focusable(m.focus) {
		return m.focus
	}
	return focusDiff
}

func (m *Model) focusable(p panel) bool {
	switch p {
	case focusTree:
		return m.sidebar() && len(m.files) > 0
	case focusCommits:
		return m.logMode && m.sidebar() && m.logSplit(m.bodyHeight()) > 0
	default:
		return true
	}
}

// cycleFocus moves focus to the next panel that can take it: diff, commits
// (history mode only), then the file tree, and back to the diff.
func (m *Model) cycleFocus() {
	order := []panel{focusDiff, focusCommits, focusTree}
	at := slices.Index(order, m.panelFocus())
	m.treeDir = ""
	for i := 1; i <= len(order); i++ {
		if p := order[(at+i)%len(order)]; m.focusable(p) {
			m.focus = p
			return
		}
	}
}

// moveLine is ↑ / ↓ and j / k: a line of the diff, a commit, or a file,
// depending on which panel has focus.
func (m *Model) moveLine(delta int) {
	switch m.panelFocus() {
	case focusCommits:
		m.loadCommit(m.commitIdx + delta)
	case focusTree:
		tree := buildTree(m.files, m.collapsed)
		next := min(max(m.treeSel(tree)+delta, 0), len(tree)-1)
		if l := tree[next]; l.file < 0 {
			m.treeDir = l.path
		} else {
			m.treeDir = ""
			m.moveTo(m.view.FileRows[l.file])
		}
	default:
		m.step(delta)
	}
}

// treeSel is the tree line the sidebar highlights: the selected directory while
// the tree has focus, otherwise the current file or the collapsed directory
// hiding it.
func (m *Model) treeSel(tree []treeLine) int {
	if m.treeDir != "" && m.panelFocus() == focusTree {
		if i := dirIndexOf(tree, m.treeDir); i >= 0 {
			return i
		}
	}
	return treeIndexOf(tree, m.currentFile())
}

// dirKey folds or unfolds the selected directory: space toggles, - folds, +
// unfolds. It reports whether a directory was selected to act on.
func (m *Model) dirKey(key string) bool {
	if m.selectedDir() == nil {
		return false
	}
	switch key {
	case "space":
		m.setCollapsed(m.treeDir, !m.collapsed[m.treeDir])
	case "-":
		m.setCollapsed(m.treeDir, true)
	case "+", "=":
		m.setCollapsed(m.treeDir, false)
	default:
		return false
	}
	return true
}

// selectedDir is the tree line for the folder the sidebar has selected, or nil
// when the selection is a file or the tree does not have focus.
func (m *Model) selectedDir() *treeLine {
	if m.treeDir == "" || m.panelFocus() != focusTree {
		return nil
	}
	tree := buildTree(m.files, m.collapsed)
	i := dirIndexOf(tree, m.treeDir)
	if i < 0 {
		return nil
	}
	return &tree[i]
}

func (m *Model) setCollapsed(path string, shut bool) {
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
	if shut {
		m.collapsed[path] = true
	} else {
		delete(m.collapsed, path)
	}
}

// Init starts following the working tree when live-follow is on; otherwise
// hunk has nothing to do before its first render.
func (m *Model) Init() tea.Cmd {
	var cmd tea.Cmd
	if m.live && m.watch != nil {
		cmd = m.watch.wait()
	}
	return m.withMarquee(cmd)
}

func (m *Model) startMarquee() tea.Cmd {
	return m.armMarquee(m.selectedNameOverflows())
}

// startMarqueeFromPaint arms the clock from the last sidebar paint, so a
// 180ms tick does not rebuild the tree just to ask whether a name overflowed.
func (m *Model) startMarqueeFromPaint() tea.Cmd {
	return m.armMarquee(m.marqueeOverflow)
}

func (m *Model) armMarquee(overflow bool) tea.Cmd {
	if m.marqueeArmed || !m.sidebar() || !overflow {
		return nil
	}
	m.marqueeArmed = true
	return tea.Tick(marqueeStep, func(time.Time) tea.Msg { return marqueeTickMsg{} })
}

func (m *Model) withMarquee(cmd tea.Cmd) tea.Cmd {
	if extra := m.startMarquee(); extra != nil {
		if cmd != nil {
			return tea.Batch(cmd, extra)
		}
		return extra
	}
	return cmd
}

func (m *Model) split() bool   { return m.wantSplit && m.contentWidth() >= minSplitWidth }
func (m *Model) sidebar() bool { return m.wantSidebar && m.width >= minSidebarWidth }

func (m *Model) contentWidth() int {
	w := m.width
	if m.wantSidebar && m.width >= minSidebarWidth {
		w -= m.sidebarW() + 1
	}
	return max(w, 1)
}

// bodyHeight is the number of diff rows on screen, leaving a line for the
// status bar.
func (m *Model) bodyHeight() int {
	return max(m.height-1, 1)
}

// Update handles resizes and key presses.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.rebuildIfNeeded()
		m.ensureVisible()
		return m, m.startMarquee()

	case marqueeTickMsg:
		m.marqueeArmed = false
		if !m.sidebar() {
			return m, nil
		}
		for k := range m.marqueeFrames {
			m.marqueeFrames[k]++
		}
		return m, m.startMarqueeFromPaint()

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseClickMsg:
		return m.handleClick(msg)

	case tea.MouseWheelMsg:
		return m.handleWheel(msg)

	case editorDoneMsg:
		// Whatever was saved should be on screen now, followed or not.
		// liveReload can move the selection via restoreCursor; wrap so a
		// newly selected name that does not fit starts scrolling without
		// waiting for a keystroke or resize.
		m.liveReload()
		if text := editorDoneToast(msg); text != "" {
			return m, m.withMarquee(m.toast(text))
		}
		return m, m.withMarquee(nil)

	case toastDoneMsg:
		// A newer toast may have replaced the one this tick was for.
		if !m.clock().Before(m.toastUntil) {
			m.toastText = ""
		}
		return m, nil

	case fsDirtyMsg:
		// The tree changed. Reload while preserving marks and cursor, then wait
		// for the next change. If following was paused since this fired, drop it.
		// withMarquee so a live-follow that lands on a truncated name arms the
		// clock instead of sitting still until the next keystroke or resize.
		if !m.live || m.watch == nil {
			return m, nil
		}
		m.liveReload()
		return m, m.withMarquee(m.watch.wait())
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.showHelp {
		// Any key closes help; there is nothing else to do while it is up.
		m.showHelp = false
		return m, nil
	}

	// While a prompt is open, keys edit its text, not the diff.
	if m.searchInput {
		m.searchKey(msg)
		return m, nil
	}
	if m.filterInput {
		m.filterKey(msg)
		return m, nil
	}

	// A keystroke means the last result has been read.
	m.msg = ""

	return m, m.withMarquee(m.command(msg.String()))
}

// command runs the action bound to a key. It is shared by the keyboard and by
// clicks on the status-bar option labels, so both do exactly the same thing.
func (m *Model) command(key string) tea.Cmd {
	if m.dirKey(key) {
		return nil
	}
	switch key {
	case "q", "ctrl+c":
		return tea.Quit

	case "j", "down":
		m.moveLine(1)
	case "k", "up":
		m.moveLine(-1)
	case "ctrl+w":
		m.cycleFocus()
	case "shift+right":
		m.resizeSidebar(1)
	case "shift+left":
		m.resizeSidebar(-1)
	case "ctrl+d", "pgdown":
		m.step(m.bodyHeight() / 2)
	case "ctrl+u", "pgup":
		m.step(-m.bodyHeight() / 2)
	case "g", "home":
		m.moveTo(0)
	case "G", "end":
		m.moveTo(len(m.view.Rows) - 1)

	case "/":
		m.searchInput, m.typed = true, ""
		m.searchPanel = m.panelFocus()
	case "n":
		// When a search is active, n repeats it (vim); otherwise it is the next
		// hunk, as always. The commits panel has its own query. Gate on the
		// panel alone so n/N do not fall through to the diff search while
		// commits are focused with an empty query; jumpCommitMatch no-ops.
		if m.panelFocus() == focusCommits {
			m.jumpCommitMatch(1)
		} else if m.search != "" {
			m.jumpMatch(1)
		} else {
			m.moveTo(NextIndex(m.view.HunkRows, m.cur))
		}
	case "N":
		if m.panelFocus() == focusCommits {
			m.jumpCommitMatch(-1)
		} else if m.search != "" {
			m.jumpMatch(-1)
		}
	case "esc":
		if m.panelFocus() == focusCommits {
			m.commitSearch = ""
		} else {
			m.search = ""
		}
	case "p":
		m.moveTo(PrevIndex(m.view.HunkRows, m.cur))
	case "]":
		m.treeDir = ""
		m.moveTo(NextIndex(m.view.FileRows, m.cur))
	case "[":
		m.treeDir = ""
		m.moveTo(PrevIndex(m.view.FileRows, m.cur))
	// Commits run newest first, so } walks back in time — the direction git log
	// prints. Both clamp at the ends rather than wrapping, like ] and [.
	case "}":
		m.loadCommit(m.commitIdx + 1)
	case "{":
		m.loadCommit(m.commitIdx - 1)

	// ponytail: horizontal scrolling instead of soft-wrap. Wrapping a
	// side-by-side view means rows stop being one screen line each, which the
	// windowing and navigation both assume. Revisit only if people ask.
	case "l", "right":
		m.hscroll += 8
	case "h", "left":
		m.hscroll = max(m.hscroll-8, 0)

	case "s":
		m.wantSplit = !m.wantSplit
		m.rebuildIfNeeded()
	case "b":
		m.wantSidebar = !m.wantSidebar
		m.focus = focusDiff
		m.rebuildIfNeeded()
	case "f":
		return m.toggleFollow()
	case "i":
		m.toggleIgnoreWS()
	case "+", "=":
		m.changeContext(1)
	case "-":
		m.changeContext(-1)
	case "W":
		m.showWS = !m.showWS
		if m.showWS {
			m.msg = "showing whitespace"
		} else {
			m.msg = "hiding whitespace"
		}
	case "F":
		m.filterInput, m.filterTyped = true, ""
	case "H":
		m.syntax = !m.syntax
		if m.syntax {
			m.msg = "syntax highlighting on"
		} else {
			m.msg = "syntax highlighting off"
		}
	case "?":
		m.showHelp = true

	case "E":
		return m.openEditor()

	case "space", "a", "d", "w", "u":
		if m.staging() {
			m.handleMarkKey(key)
		}
	}
	return nil
}

// toggleFollow pauses or resumes live-follow. Resuming reloads once right away
// so the screen catches up on whatever changed while it was paused, then re-arms
// the watcher.
func (m *Model) toggleFollow() tea.Cmd {
	if m.watch == nil {
		return nil
	}
	m.live = !m.live
	if !m.live {
		m.msg = "following paused — f to resume"
		return nil
	}
	m.liveReload()
	if m.msg == "" {
		m.msg = "following resumed"
	}
	return m.watch.wait()
}

// maxContext caps how far + will widen the context. Past this a "diff" is just
// the whole file twice.
const maxContext = 20

// changeContext widens or narrows the unchanged lines around each hunk and
// re-diffs. Git modes only, since they are the only sources hunk can re-run.
func (m *Model) changeContext(delta int) {
	if m.repo == nil {
		return
	}
	next := min(max(m.context+delta, 0), maxContext)
	if next == m.context {
		return
	}
	m.context = next
	m.rediff()
	m.msg = "context: " + plural(m.context, "line")
}

// toggleIgnoreWS flips whitespace-only changes on and off by re-running the
// diff. Only a git mode can re-source, so it is a no-op elsewhere.
func (m *Model) toggleIgnoreWS() {
	if m.repo == nil {
		return
	}
	m.ignoreWS = !m.ignoreWS
	m.rediff()
	if m.ignoreWS {
		m.msg = "ignoring whitespace"
	} else {
		m.msg = "showing whitespace"
	}
}

// searchKey edits the / prompt. Enter confirms and jumps to the first match,
// esc abandons the edit (the previous search stays), backspace deletes.
func (m *Model) searchKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc":
		m.searchInput, m.typed = false, ""
	case "enter":
		m.searchInput = false
		if m.searchPanel == focusCommits {
			m.commitSearch = m.typed
			if m.commitSearch != "" {
				m.jumpCommitMatchFrom(m.commitIdx, 1, true)
			}
		} else {
			m.search = m.typed
			if m.search != "" {
				m.jumpMatchFrom(m.cur, 1, true)
			}
		}
	case "backspace":
		if r := []rune(m.typed); len(r) > 0 {
			m.typed = string(r[:len(r)-1])
		}
	default:
		// Key.Text is non-empty only for printable input, so control keys are
		// ignored here without a list of names to maintain.
		m.typed += msg.Key().Text
	}
}

// filterKey edits the F prompt. Enter compiles the regex and hides matching
// hunks (an empty pattern clears the filter); esc abandons the edit; a bad
// pattern is reported and the old filter stays.
func (m *Model) filterKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc":
		m.filterInput, m.filterTyped = false, ""
	case "enter":
		m.filterInput = false
		m.setFilter(m.filterTyped)
	case "backspace":
		if r := []rune(m.filterTyped); len(r) > 0 {
			m.filterTyped = string(r[:len(r)-1])
		}
	default:
		m.filterTyped += msg.Key().Text
	}
}

// setFilter swaps the active regex and re-derives the visible diff, keeping the
// marks and cursor that still exist afterwards.
func (m *Model) setFilter(src string) {
	var re *regexp.Regexp
	if src != "" {
		var err error
		if re, err = regexp.Compile(src); err != nil {
			m.msg = "bad filter: " + firstLine(err.Error())
			return
		}
	}

	where := m.cursorIdentity()
	snap := m.snapshotMarks()
	m.filter, m.filterSrc = re, src
	m.applyFilter()
	m.restoreMarks(snap)
	m.rebuildView()
	m.restoreCursor(where)

	switch re {
	case nil:
		m.msg = "filter cleared"
	default:
		m.msg = "filtering out /" + src + "/"
	}
}

// jumpMatch moves to the next (dir 1) or previous (dir -1) row matching the
// active search, wrapping around the diff.
func (m *Model) jumpMatch(dir int) { m.jumpMatchFrom(m.cur, dir, false) }

func (m *Model) jumpMatchFrom(from, dir int, inclusive bool) {
	n := len(m.view.Rows)
	if n == 0 || m.search == "" {
		return
	}
	idx := searchWrap(n, from, dir, inclusive, func(i int) bool {
		return matchText(m.rowSearchText(i), m.search)
	})
	if idx < 0 {
		m.msg = "no match: " + m.search
		return
	}
	m.moveTo(idx)
}

func (m *Model) jumpCommitMatch(dir int) { m.jumpCommitMatchFrom(m.commitIdx, dir, false) }

func (m *Model) jumpCommitMatchFrom(from, dir int, inclusive bool) {
	n := len(m.commits)
	if n == 0 || m.commitSearch == "" {
		return
	}
	idx := searchWrap(n, from, dir, inclusive, func(i int) bool {
		return matchText(commitSearchText(m.commits[i]), m.commitSearch)
	})
	if idx < 0 {
		m.msg = "no match: " + m.commitSearch
		return
	}
	m.loadCommit(idx)
}

// searchWrap walks n items from start in dir, wrapping with the same modulo
// indexing both commit and diff search use. inclusive includes start;
// otherwise the walk begins at start+dir. Returns the first match, or -1.
func searchWrap(n, start, dir int, inclusive bool, match func(int) bool) int {
	if n == 0 {
		return -1
	}
	if !inclusive {
		start += dir
	}
	for i := 0; i < n; i++ {
		idx := ((start+dir*i)%n + n) % n
		if match(idx) {
			return idx
		}
	}
	return -1
}

func commitSearchText(c git.Commit) string {
	return c.Subject + " " + c.Author
}

// rowSearchText is everything on a row a search can hit: its header text and
// both panes.
func (m *Model) rowSearchText(idx int) string {
	r := m.view.Rows[idx]
	return r.Text + " " + r.Left.Text + " " + r.Right.Text
}

// matchText is smartcase: a lowercase query matches case-insensitively, a query
// with any uppercase is matched exactly — the same rule vim uses.
func matchText(hay, needle string) bool {
	if needle == "" {
		return false
	}
	for _, r := range needle {
		if unicode.IsUpper(r) {
			return strings.Contains(hay, needle)
		}
	}
	return strings.Contains(strings.ToLower(hay), needle)
}

// handleClick routes a left click to whatever is under the pointer: an option
// on the status bar, a file in the sidebar, or a row in the diff body.
func (m *Model) handleClick(e tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if e.Button != tea.MouseLeft {
		return m, nil
	}
	// A click, like a keystroke, dismisses the help overlay and does nothing
	// else while it is up.
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}
	m.msg = ""

	x, y := e.X, e.Y

	// Status bar: the bottom line. A click on an option label runs its key.
	if y == m.bodyHeight() {
		for _, h := range m.hints {
			if x >= h.x0 && x <= h.x1 {
				return m, m.withMarquee(m.command(h.key))
			}
		}
		return m, m.startMarquee()
	}

	// Sidebar: the left column, when shown. A click jumps to that file, or in
	// history mode to that commit.
	if m.sidebar() && x < m.sidebarW() {
		if m.logMode {
			ci, fi := m.logSidebarAt(y)
			switch {
			case ci >= 0:
				m.focus = focusCommits
				m.loadCommit(ci)
			case fi >= 0:
				m.focus, m.treeDir = focusTree, ""
				m.moveTo(m.view.FileRows[fi])
			}
			return m, m.startMarquee()
		}
		if idx := m.sidebarFileAt(y); idx >= 0 {
			m.focus, m.treeDir = focusTree, ""
			m.moveTo(m.view.FileRows[idx])
		}
		return m, m.startMarquee()
	}

	// Body: put the cursor on the clicked row so the next mark or jump acts on
	// what the user pointed at.
	m.focus = focusDiff
	if row := m.top + y; row < len(m.view.Rows) {
		m.moveTo(row)
	}
	return m, m.startMarquee()
}

// sidebarFileAt maps a body-row y to the file index drawn there, or -1 for a
// directory or a blank line past the end. It mirrors renderSidebar.
func (m *Model) sidebarFileAt(y int) int {
	return m.treeFileAt(y, m.bodyHeight())
}

// treeFileAt maps row y of a tree window h rows tall to the file drawn there,
// or -1 for a directory or a blank row.
func (m *Model) treeFileAt(y, h int) int {
	tree := buildTree(m.files, m.collapsed)
	idx := listStart(m.treeSel(tree), h) + y
	if y < 0 || idx >= len(tree) {
		return -1
	}
	return tree[idx].file
}

// handleWheel scrolls the diff a few lines per notch, the shape people expect
// from a pager.
func (m *Model) handleWheel(e tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	const rows = 3
	switch e.Button {
	case tea.MouseWheelUp:
		m.step(-rows)
	case tea.MouseWheelDown:
		m.step(rows)
	}
	return m, nil
}

// handleMarkKey applies the marking keys, which only exist in git review mode.
func (m *Model) handleMarkKey(key string) {
	// On a folder, a and d do what they always do, one level up: every file
	// under it, rather than every hunk in one file. Space is the folder's fold
	// toggle and never reaches here — marking a whole subtree is too much for
	// the key that marks a single hunk.
	// w and u still work from a folder row, so only a and d are taken here.
	if dir := m.selectedDir(); dir != nil && (key == "a" || key == "d") {
		for i := dir.lo; i < dir.hi; i++ {
			m.markWholeFile(i, key == "a")
		}
		return
	}

	file, hunk := m.currentTarget()

	switch key {
	case "space":
		if m.heldSpace(file, hunk) {
			return
		}
		// Marking moves on to the next hunk in this file: approving is a run of
		// decisions, and stopping on each one you just made costs a keystroke.
		// Unmarking stays put, so you can see what you took back.
		on := !m.marks.has(file, hunk)
		m.marks.set(file, hunk, on)
		if on {
			m.nextHunkInFile(file)
		}
	case "a":
		m.markWholeFile(file, true)
	case "d":
		m.markWholeFile(file, false)
	case "w":
		if hunks, _ := m.marks.total(); hunks == 0 {
			m.msg = "nothing marked — space marks a hunk, a the whole file"
			return
		}
		m.stage()
	case "u":
		m.undo()
	}
}

// heldSpace reports whether this space press is the key repeating: it landed on
// a hunk the cursor was moved to by the previous press, too soon after it to be
// a decision. A press on the hunk just marked is always a decision, so undoing
// a mark stays instant.
func (m *Model) heldSpace(file, hunk int) bool {
	now := m.clock()
	target := [2]int{file, hunk}
	if target != m.lastMark && now.Sub(m.lastMarkAt) < markRepeat {
		return true
	}
	m.lastMark, m.lastMarkAt = target, now
	return false
}

// nextHunkInFile puts the cursor on this file's next hunk, or leaves it where
// it is when this was the file's last one — jumping into the next file would
// take the eye somewhere it did not ask to go.
func (m *Model) nextHunkInFile(file int) {
	for _, row := range m.view.HunkRows {
		if row > m.cur && m.view.Rows[row].FileIdx == file {
			m.moveTo(row)
			return
		}
	}
}

// place describes where the cursor sits by content, not row number, so it can
// be found again after the view is rebuilt. key is empty when the cursor is on
// a file header rather than a hunk; offset is how far into that hunk the cursor
// had scrolled, and screen how far down the window it was sitting, so a reload
// puts the same lines back under the same eyes instead of yanking the view up
// to the hunk's first row.
type place struct {
	path   string
	key    string
	offset int
	screen int
}

func (m *Model) cursorIdentity() place {
	if m.cur >= len(m.view.Rows) {
		return place{}
	}
	r := m.view.Rows[m.cur]
	if r.FileIdx >= len(m.files) {
		return place{}
	}
	f := m.files[r.FileIdx]
	p := place{path: f.Path(), screen: m.cur - m.top}
	if r.HunkIdx >= 0 && r.HunkIdx < len(f.Hunks) {
		p.key = f.Hunks[r.HunkIdx].Key()
		lo, _ := m.currentHunkSpan()
		if lo >= 0 {
			p.offset = m.cur - lo
		}
	}
	return p
}

// restoreCursor puts the cursor back where it was reading: same file, same
// hunk, same distance into that hunk, and the same distance down the window.
func (m *Model) restoreCursor(p place) {
	fi := m.fileIndexByPath(p.path)
	if fi < 0 {
		m.moveTo(m.cur) // path gone; just clamp and stay near where we were
		return
	}

	target := m.view.FileRows[fi]
	if p.key != "" {
		globalHunk := 0
		for i := 0; i < fi; i++ {
			globalHunk += len(m.files[i].Hunks)
		}
		for hi, h := range m.files[fi].Hunks {
			if h.Key() == p.key {
				if g := globalHunk + hi; g < len(m.view.HunkRows) {
					target = m.view.HunkRows[g] + p.offset
					// The hunk may have shrunk under the offset; never walk out
					// of it into the next one.
					if end := hunkEnd(m.view, m.view.HunkRows[g]); target >= end {
						target = end - 1
					}
				}
				break
			}
		}
	}

	m.moveTo(target)
	if p.screen > 0 {
		m.top = m.cur - p.screen
		m.ensureVisible()
	}
}

// hunkEnd is the row after the last one belonging to the hunk that starts at
// row start.
func hunkEnd(v *View, start int) int {
	r := v.Rows[start]
	end := start + 1
	for end < len(v.Rows) && v.Rows[end].FileIdx == r.FileIdx && v.Rows[end].HunkIdx == r.HunkIdx {
		end++
	}
	return end
}

func (m *Model) fileIndexByPath(path string) int {
	for i, f := range m.files {
		if f.Path() == path {
			return i
		}
	}
	return -1
}

// currentTarget is the file and hunk the cursor is on. A file with no hunks
// (a binary file) can only be marked whole.
func (m *Model) currentTarget() (file, hunk int) {
	if m.cur >= len(m.view.Rows) {
		return 0, wholeFile
	}
	row := m.view.Rows[m.cur]
	if row.HunkIdx < 0 || len(m.files[row.FileIdx].Hunks) == 0 {
		return row.FileIdx, wholeFile
	}
	return row.FileIdx, row.HunkIdx
}

func (m *Model) markWholeFile(file int, on bool) {
	f := m.files[file]
	if len(f.Hunks) == 0 {
		m.marks.set(file, wholeFile, on)
		return
	}
	for i := range f.Hunks {
		m.marks.set(file, i, on)
	}
}

// stage writes the marked hunks straight to the index — no confirmation, since
// nothing here touches the working tree and "u" takes it right back out. The
// staged hunks then drop off the diff, freeing the screen for what is left.
func (m *Model) stage() {
	result, err := m.stageMarked()
	if err != nil {
		// Keep the marks: the user can fix the problem and try again.
		m.msg = "staging failed: " + firstLine(err.Error())
		return
	}
	if err := m.reload(); err != nil {
		m.msg = result + " (could not re-read the working tree: " + firstLine(err.Error()) + ")"
		return
	}
	m.msg = result + "  ·  u to undo"
}

// undo reverses the most recent stage, putting those changes back into the
// working tree exactly as they were before w. Earlier stages stay on the
// stack so another "u" can reverse them too.
func (m *Model) undo() {
	if len(m.undoStack) == 0 {
		m.msg = "nothing to undo"
		return
	}
	skipped := false
	if err := m.unstageLast(); err != nil {
		if !errors.Is(err, errSkippedStuckUndo) {
			m.msg = "undo failed: " + firstLine(err.Error())
			return
		}
		skipped = true
	}
	if err := m.reload(); err != nil {
		if skipped {
			m.msg = "skipped stuck undo (could not re-read the working tree: " + firstLine(err.Error()) + ")"
			return
		}
		m.msg = "undone (could not re-read the working tree: " + firstLine(err.Error()) + ")"
		return
	}
	if skipped {
		if len(m.undoStack) > 0 {
			m.msg = "skipped stuck undo  ·  u to undo more"
			return
		}
		m.msg = "skipped stuck undo"
		return
	}
	if len(m.undoStack) > 0 {
		m.msg = "undone — back to unstaged  ·  u to undo more"
		return
	}
	m.msg = "undone — back to unstaged"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// step scrolls within the current file. Only one file is on screen at a time,
// so running off its end should stop at the end rather than drag the view into
// a file the reader did not ask for — ] and [ are how you change file.
func (m *Model) step(delta int) {
	lo, hi := m.fileSpan(m.cur)
	m.moveTo(min(max(m.cur+delta, lo), hi-1))
}

// moveTo puts the cursor on a row, clamped to the diff, and scrolls to it.
func (m *Model) moveTo(row int) {
	if len(m.view.Rows) == 0 {
		m.cur, m.top = 0, 0
		return
	}
	m.cur = min(max(row, 0), len(m.view.Rows)-1)
	m.ensureVisible()
}

func (m *Model) ensureVisible() {
	// The viewport is scoped to the current file: only its rows are ever shown,
	// so scrolling can never mix two files on screen.
	lo, hi := m.fileSpan(m.cur)
	h := m.bodyHeight()
	if m.cur < m.top {
		m.top = m.cur
	}
	if m.cur >= m.top+h {
		m.top = m.cur - h + 1
	}
	m.top = min(max(m.top, lo), max(hi-h, lo))
}

// fileSpan is the [lo, hi) row range of the file that owns row. Rows outside it
// belong to other files and are never drawn while this one is current.
func (m *Model) fileSpan(row int) (lo, hi int) {
	if len(m.view.Rows) == 0 {
		return 0, 0
	}
	row = min(row, len(m.view.Rows)-1)
	f := m.view.Rows[row].FileIdx
	lo = m.view.FileRows[f]
	if f+1 < len(m.view.FileRows) {
		hi = m.view.FileRows[f+1]
	} else {
		hi = len(m.view.Rows)
	}
	return lo, hi
}

// currentHunkSpan is the [lo, hi) row range of the hunk under the cursor, or
// (-1, -1) when the cursor is not on a hunk (a file header or a hunkless file).
func (m *Model) currentHunkSpan() (lo, hi int) {
	file, hunk := m.currentTarget()
	if hunk == wholeFile || m.cur >= len(m.view.Rows) {
		return -1, -1
	}
	match := func(i int) bool {
		r := m.view.Rows[i]
		return r.FileIdx == file && r.HunkIdx == hunk
	}
	lo, hi = m.cur, m.cur+1
	for lo-1 >= 0 && match(lo-1) {
		lo--
	}
	for hi < len(m.view.Rows) && match(hi) {
		hi++
	}
	return lo, hi
}

// rebuildIfNeeded re-flattens the diff when the split/unified choice changes,
// keeping the cursor on the same file rather than at the same row number.
func (m *Model) rebuildIfNeeded() {
	if m.split() == m.builtSplit {
		return
	}
	where := m.cursorIdentity()
	m.builtSplit = m.split()
	m.rebuildView()
	m.restoreCursor(where)
}

// View renders the current screen into the alternate screen buffer.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	// Cell-motion mouse tracking delivers clicks and wheel events so the sidebar
	// and status-bar options work by pointer, not only by keystroke.
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *Model) render() string {
	if m.width == 0 || m.height == 0 {
		return "" // no size yet; the first WindowSizeMsg is on its way
	}

	screen := m.renderScreen()
	if m.showHelp {
		// The help is a modal: the diff stays visible behind a centered box.
		box := m.renderHelp()
		screen = m.float(screen, box, (m.width-lipgloss.Width(box))/2, (m.height-lipgloss.Height(box))/2)
	}
	if m.toastText != "" {
		box := m.st.toast.Render(clip(m.toastText, max(m.width-4, 1)))
		screen = m.float(screen, box, (m.width-lipgloss.Width(box))/2, (m.height-lipgloss.Height(box))/2)
	}
	return screen
}

// renderScreen draws the diff, sidebar, and status bar — the whole screen
// except any modal floating on top of it.
func (m *Model) renderScreen() string {
	body := m.renderBody()
	side := m.renderSidebar()

	// Outside history mode, where the headers say it, the rule lights up while
	// the tree has focus.
	sep := m.st.gutter.Render("│")
	if !m.logMode && m.panelFocus() == focusTree {
		sep = m.st.focusRail.Render("│")
	}

	lines := make([]string, 0, m.bodyHeight()+1)
	for i := 0; i < m.bodyHeight(); i++ {
		line := body[i]
		if side != nil {
			line = side[i] + sep + line
		}
		lines = append(lines, line)
	}
	lines = append(lines, m.renderStatus())
	return strings.Join(lines, "\n")
}

// float draws box over base at column x, row y, compositing so the base
// screen shows through around it.
func (m *Model) float(base, box string, x, y int) string {
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(max(x, 0)).Y(max(y, 0)).Z(1),
	).Render()
}

// toastFor is how long a toast stays on screen.
const toastFor = 4 * time.Second

// toastDoneMsg is the tick that takes a toast back down.
type toastDoneMsg struct{}

// toast floats text in a box centered on screen for a few seconds. It is for
// things the user has to notice, where the status line is too easy to miss.
func (m *Model) toast(text string) tea.Cmd {
	m.toastText, m.toastUntil = text, m.clock().Add(toastFor)
	return tea.Tick(toastFor, func(time.Time) tea.Msg { return toastDoneMsg{} })
}

// renderBody renders exactly the visible window of rows. Everything off screen
// costs nothing, which is what keeps a 20k-line diff responsive.
func (m *Model) renderBody() []string {
	// One column is reserved for the cursor marker: without it, j/k move
	// something invisible and navigation feels broken.
	w := m.contentWidth() - 1
	out := make([]string, 0, m.bodyHeight())

	if len(m.view.Rows) == 0 {
		return m.renderEmpty(w + 1)
	}

	// Only the current file's rows are drawn; anything past its end is blank,
	// even when that leaves empty space, so files never mix on screen.
	_, hi := m.fileSpan(m.cur)
	hlo, hhi := m.currentHunkSpan()
	for i := 0; i < m.bodyHeight(); i++ {
		row := m.top + i
		if row >= hi || row >= len(m.view.Rows) {
			out = append(out, m.st.base.Render(strings.Repeat(" ", w+1)))
			continue
		}
		focus := hlo <= row && row < hhi
		out = append(out, m.railMark(row, hlo, hhi)+m.renderRow(m.view.Rows[row], w, focus))
	}
	return out
}

// renderEmpty fills the body when there is no diff to show. In a repo that is
// not "done" but "not yet": hunk keeps following the tree, so the message says
// what it is waiting for.
func (m *Model) renderEmpty(w int) []string {
	msg := "no changes to show"
	switch {
	case m.logMode:
		msg = "this commit changes nothing — } and { move to another"
	case m.staging() && m.live:
		msg = "nothing to stage yet — watching for edits"
	case m.staging() && m.watch != nil:
		msg = "nothing to stage — following is paused, press f to resume"
	case m.staging():
		msg = "nothing to stage — the working tree is clean"
	}

	blank := m.st.base.Render(strings.Repeat(" ", w))
	out := make([]string, 0, m.bodyHeight())
	mid := m.bodyHeight() / 3
	for i := 0; i < m.bodyHeight(); i++ {
		if i != mid {
			out = append(out, blank)
			continue
		}
		pad := max((w-lipgloss.Width(msg))/2, 0)
		out = append(out, fit(m.st.base.Render(strings.Repeat(" ", pad))+m.st.notice.Render(msg), 0, w, m.st.base))
	}
	return out
}

// railMark draws the left-margin column for a row: the cursor bar on the cursor
// row, a thin accent bar down the active hunk, and a green bar down any marked
// hunk so what will be staged is visible at a glance. Green always means marked.
func (m *Model) railMark(row, hlo, hhi int) string {
	cursor := row == m.cur
	current := hlo <= row && row < hhi
	marked := m.rowMarked(row)

	// A marked hunk gets a solid bar so it reads as a block; the current hunk
	// gets a thin accent. The cursor is always solid so its line is findable.
	glyph := "▎"
	if cursor || marked {
		glyph = "▌"
	}

	// Marked wins the color outright — a whole marked hunk reads green top to
	// bottom, including the cursor line, so "this will be staged" is never
	// masked by the cursor or the current-hunk accent.
	switch {
	case marked:
		return m.st.railMarked.Render(glyph)
	case cursor:
		return m.st.cursor.Render(glyph)
	case current:
		return m.st.focusRail.Render(glyph)
	default:
		return m.st.base.Render(" ")
	}
}

// rowMarked reports whether the hunk a row belongs to is currently marked.
func (m *Model) rowMarked(row int) bool {
	if row < 0 || row >= len(m.view.Rows) {
		return false
	}
	r := m.view.Rows[row]
	return m.marks.has(r.FileIdx, r.HunkIdx)
}

func (m *Model) renderRow(r Row, w int, focus bool) string {
	switch r.Kind {
	case RowFile:
		return fit(m.st.fileHeader.Render(" "+r.Text), 0, w, m.st.fileHeader)
	case RowHunk:
		style := m.st.hunkHeader
		if focus {
			style = m.st.focusHeader
		}
		return fit(style.Render(" "+r.Text), 0, w, style)
	case RowNotice:
		return fit(m.st.notice.Render("   "+r.Text), 0, w, m.st.base)
	case RowSpacer:
		return m.st.base.Render(strings.Repeat(" ", w))
	}

	// The two outermost columns belong to the change block's outline. They are
	// reserved on every row, boxed or not, or text would shift sideways as the
	// eye moves from a context line into a change.
	inner := w - 2
	lead := m.st.box.Render(edgeGlyph(r.BoxLeft, true))
	trail := m.st.box.Render(edgeGlyph(r.BoxRight, false))
	hl := m.highlightFor(r)

	if !m.builtSplit {
		return lead + m.paneOr(r.BoxLeft, inner, func() string {
			return m.renderUnifiedRow(r, inner, hl)
		}) + trail
	}

	half := (inner - 1) / 2
	left := m.paneOr(r.BoxLeft, half, func() string {
		return m.st.renderSide(r.Left, "", numWidth, half-numWidth-1, m.hscroll, m.showWS, hl)
	})
	right := m.paneOr(r.BoxRight, inner-half-1, func() string {
		return m.st.renderSide(r.Right, "", numWidth, inner-half-1-numWidth-1, m.hscroll, m.showWS, hl)
	})
	return lead + left + m.divider(r) + right + trail
}

// highlightFor returns the per-line syntax colorer for a row's file, or nil when
// highlighting is off or no lexer will match.
func (m *Model) highlightFor(r Row) func(string) []synSpan {
	if !m.syntax || m.hl == nil || r.FileIdx < 0 || r.FileIdx >= len(m.files) {
		return nil
	}
	path := m.files[r.FileIdx].Path()
	return func(text string) []synSpan { return m.hl.spans(path, text) }
}

// paneOr draws a pane's share of a rule row, or the pane's normal contents when
// the outline is not opening or closing here.
func (m *Model) paneOr(p BoxPart, width int, draw func() string) string {
	if p == BoxTop || p == BoxBottom {
		return m.st.box.Render(strings.Repeat(lipgloss.RoundedBorder().Top, width))
	}
	return draw()
}

// edgeGlyph is the outline's outer column for one pane: a corner where the box
// opens or closes, its side while it is open, nothing when the pane is outside.
func edgeGlyph(p BoxPart, left bool) string {
	b := lipgloss.RoundedBorder()
	switch p {
	case BoxTop:
		if left {
			return b.TopLeft
		}
		return b.TopRight
	case BoxMid:
		return b.Left
	case BoxBottom:
		if left {
			return b.BottomLeft
		}
		return b.BottomRight
	default:
		return " "
	}
}

// divider draws the column between the panes. Inside a block there is no
// divider: the outline is one shape, so the seam carries whatever the outline
// is doing on that row — the rule sweeping across, a corner where a box that
// covers only one pane turns, the turn down into the taller pane, that pane's
// wall, or the corner where it finally closes.
func (m *Model) divider(r Row) string {
	b := lipgloss.RoundedBorder()
	l, rt := r.BoxLeft, r.BoxRight

	if l == BoxNone && rt == BoxNone {
		return m.st.gutter.Render("│")
	}
	if r.Arrow {
		// The change reads left to right — old on the left, new on the right —
		// and the marker says so, floating in the gap inside the outline.
		return m.st.box.Bold(true).Render("→")
	}

	glyph := b.Left // one pane is enclosed and the other is not: its wall
	switch {
	case l == rt: // both panes do the same thing here
		switch l {
		case BoxTop, BoxBottom:
			glyph = b.Top // one rule sweeping across both panes
		default:
			glyph = " " // inside the box, nothing separates the panes
		}
	case l == BoxTop:
		glyph = b.TopRight // a box over the left pane only
	case rt == BoxTop:
		glyph = b.TopLeft
	case l == BoxBottom && rt == BoxNone:
		glyph = b.BottomRight
	case rt == BoxBottom && l == BoxNone:
		glyph = b.BottomLeft
	case l == BoxBottom:
		glyph = b.TopRight // the left pane closes; its rule turns down into the
	case rt == BoxBottom: // wall the taller pane leans on, and vice versa
		glyph = b.TopLeft
	}
	return m.st.box.Render(glyph)
}

// renderUnifiedRow shows a single column with +/-/space signs, the shape people
// already know from git diff.
func (m *Model) renderUnifiedRow(r Row, w int, hl func(string) []synSpan) string {
	side, sign := r.Left, " "
	if r.Left.Empty {
		side, sign = r.Right, "+"
	} else if r.Right.Empty && r.Left.Kind == diff.Removed {
		sign = "-"
	} else if r.Left.Kind == diff.Added {
		sign = "+"
	}
	return m.st.renderSide(side, sign, numWidth, w-numWidth-1, m.hscroll, m.showWS, hl)
}

// renderSidebar draws the changed files as a tree, or nil when there is no room
// for it.
func (m *Model) renderSidebar() []string {
	if !m.sidebar() {
		clear(m.marqueeFrames)
		clear(m.marqueeSeen)
		m.marqueeOverflow = false
		return nil
	}
	m.marqueeOverflow = false
	defer m.pruneMarquee()

	h, w := m.bodyHeight(), m.sidebarW()
	if m.logMode {
		return m.renderLogSidebar(h, w)
	}
	return m.renderTree(h, w)
}

// renderTree draws h rows of the file tree, scrolled so the current file shows.
func (m *Model) renderTree(h, w int) []string {
	tree := buildTree(m.files, m.collapsed)
	sel := m.treeSel(tree)
	start := listStart(sel, h)
	out := make([]string, 0, h)
	for i := 0; i < h; i++ {
		out = append(out, m.treeRow(tree, start+i, w, start+i == sel))
	}
	return out
}

// currentFile is the file the cursor is in.
func (m *Model) currentFile() int {
	if m.cur < len(m.view.Rows) {
		return m.view.Rows[m.cur].FileIdx
	}
	return 0
}

// listStart is the first index a list of h rows should draw so that the
// selected one stays on screen.
func listStart(cur, h int) int {
	if h > 0 && cur >= h {
		return cur - h + 1
	}
	return 0
}

// treeRow renders one line of the tree, or a blank row past the end. A selected
// file gets the accent; a selected directory only a muted bar, since it has no
// diff of its own, plus the - or + that space would do to it. A collapsed
// directory always shows its +. A directory turns green once every file under
// it is approved.
func (m *Model) treeRow(tree []treeLine, idx, w int, sel bool) string {
	if idx < 0 || idx >= len(tree) {
		return m.st.sidebar.Render(strings.Repeat(" ", w))
	}
	l := tree[idx]
	style := m.st.sidebar

	if l.file < 0 {
		if sel {
			style = m.st.sidebarDirSel
		}
		name := style
		if m.allApproved(l.lo, l.hi) {
			name = style.Foreground(m.st.stagedFg)
		}
		// The glyph takes the place of the connector's dash, so nothing shifts.
		conn := l.prefix[len(l.prefix)-len("├─"):]
		switch {
		case m.collapsed[l.path]:
			conn = strings.TrimSuffix(conn, "─") + "+"
		case sel:
			conn = strings.TrimSuffix(conn, "─") + "-"
		}
		room := m.treeNameRoom(l, w)
		line := style.Render(" "+l.prefix[:len(l.prefix)-len("├─")]) +
			name.Render(conn+m.clipOrMarquee("dir:"+l.path, l.name, room, sel)+"/")
		return fit(line, 0, w, style)
	}

	f := m.files[l.file]
	if sel {
		style = m.st.sidebarSel
	}
	adds := fmt.Sprintf("+%d", f.Added)
	dels := fmt.Sprintf(" -%d", f.Removed)
	// The green/red counts are unreadable on the selected row's accent
	// background, so there they take the selection's own foreground.
	counts := style.Render(adds + dels)
	if !sel {
		counts = style.Foreground(m.st.addedFg).Render(adds) +
			style.Foreground(m.st.removedFg).Render(dels)
	}
	lead := style.Render(" " + l.prefix)
	if m.staging() {
		symbol, symStyle := m.fileGlyph(l.file, f, style)
		lead += symStyle.Render(symbol) + style.Render(" ")
	}
	// The counts sit flush right, so the name takes what is left minus a gap.
	room := m.treeNameRoom(l, w)
	name := m.clipOrMarquee("file:"+f.Path(), l.name, room, sel)
	gap := max(w-lipgloss.Width(lead+name)-lipgloss.Width(adds+dels), 1)
	return fit(lead+style.Render(name+strings.Repeat(" ", gap))+counts, 0, w, style)
}

// approved reports whether a file needs nothing more from the reviewer: every
// hunk left is marked, or it is staged with nothing left over.
func (m *Model) approved(i int) bool {
	f := m.files[i]
	p := f.Path()
	return m.marks.state(i, f) == fileAllMarked || m.staged[p] && !m.unstaged[p]
}

// allApproved reports whether every file in [lo, hi) is approved.
func (m *Model) allApproved(lo, hi int) bool {
	for i := lo; i < hi; i++ {
		if !m.approved(i) {
			return false
		}
	}
	return hi > lo
}

// fileGlyph is the sidebar symbol for a file and the style to draw it in.
// A pending selection shows the mark symbol; otherwise a staged file shows a
// check — green when fully staged, gray when only partly.
func (m *Model) fileGlyph(idx int, f diff.File, base lipgloss.Style) (string, lipgloss.Style) {
	if m.marks.inFile(idx) > 0 {
		return m.marks.state(idx, f).symbol(), base
	}
	path := f.Path()
	switch {
	case m.staged[path] && !m.unstaged[path]:
		return "✓", base.Foreground(m.st.stagedFg)
	case m.staged[path]:
		return "✓", base.Foreground(m.st.partialFg)
	default:
		return "·", base
	}
}

func (m *Model) renderStatus() string {
	// No hints are clickable unless the option row below actually draws them.
	m.hints = nil

	// A prompt takes over the status line while it is open.
	if m.searchInput {
		return fit(m.st.statusbar.Render(" /"+m.typed+"█"), 0, m.width, m.st.statusbar)
	}
	if m.filterInput {
		return fit(m.st.statusbar.Render(" filter out: "+m.filterTyped+"█"), 0, m.width, m.st.statusbar)
	}

	// A message — a confirmation prompt, or the result of staging — replaces the
	// status line while it is relevant.
	if m.msg != "" {
		return fit(m.st.statusbar.Render(" "+m.msg), 0, m.width, m.st.statusbar)
	}
	if len(m.view.Rows) == 0 {
		if m.logMode && m.filter == nil {
			c := m.commit()
			status := fmt.Sprintf(" %s  %s  ·  nothing in this commit  ·  commit %d/%d",
				c.Short, clip(c.Subject, 40), m.commitIdx+1, len(m.commits))
			return fit(m.st.statusbar.Render(status+"  ·  }/{ commit  ·  q quit"), 0, m.width, m.st.statusbar)
		}
		if m.filter != nil {
			return fit(m.st.statusbar.Render(" everything is filtered out by /"+m.filterSrc+"/  ·  F filter  ·  q quit"), 0, m.width, m.st.statusbar)
		}
		if m.staging() {
			status := " working tree clean"
			if m.ignoreWS {
				status += "  ·  ≈ ws"
			}
			if m.watch != nil {
				live := "○ paused"
				if m.live {
					live = "● live"
				}
				status += "  ·  " + live + "  ·  f follow"
			}
			return fit(m.st.statusbar.Render(status+"  ·  q quit"), 0, m.width, m.st.statusbar)
		}
		return fit(m.st.statusbar.Render(" no changes  ·  q quit"), 0, m.width, m.st.statusbar)
	}

	fileIdx := m.view.Rows[m.cur].FileIdx
	f := m.files[fileIdx]

	mode := "split"
	if !m.builtSplit {
		mode = "unified"
	}

	left := fmt.Sprintf(" %s  +%d -%d  ·  file %d/%d  ·  %s",
		f.Path(), f.Added, f.Removed, fileIdx+1, len(m.files), mode)
	if m.search != "" {
		left += "  ·  /" + m.search
	}
	if m.showWS {
		left += "  ·  ·→"
	}
	if m.filter != nil {
		left += "  ·  ⊘ /" + m.filterSrc + "/"
	}
	opts := []hintZone{
		{key: "n"}, {key: "]"}, {key: "s"}, {key: "/"}, {key: "W"}, {key: "F"}, {key: "H"}, {key: "?"}, {key: "q"},
	}
	labels := []string{"n/p hunk", "]/[ file", "s split", "/ search", "W space", "F filter", "H syntax", "? help", "q quit"}
	if m.logMode {
		// A narrower set of hints than the other modes: the commit one has to
		// fit, and what it displaces (W, F, i, +/-) is still in the help.
		opts = []hintZone{{key: "}"}, {key: "]"}, {key: "n"}, {key: "s"}, {key: "/"}, {key: "H"}, {key: "?"}, {key: "q"}}
		labels = []string{"}/{ commit", "]/[ file", "n/p hunk", "s split", "/ search", "H syntax", "? help", "q quit"}
	}
	if m.staging() {
		if hunks, files := m.marks.total(); hunks > 0 {
			left += fmt.Sprintf("  ·  %s marked in %s", plural(hunks, "hunk"), plural(files, "file"))
		}
		if m.watch != nil {
			ind := "○ paused"
			if m.live {
				ind = "● live"
			}
			left += "  ·  " + ind
		}
		if m.ignoreWS {
			left += "  ·  ≈ ws"
		}
		if m.context != diff.DefaultContext {
			left += fmt.Sprintf("  ·  ⋯ %d", m.context)
		}
		opts = []hintZone{{key: "space"}, {key: "a"}, {key: "w"}, {key: "u"}, {key: "f"}, {key: "i"}, {key: "+"}, {key: "W"}, {key: "H"}, {key: "/"}, {key: "F"}, {key: "?"}, {key: "q"}}
		labels = []string{"space mark", "a/d file", "w stage", "u undo", "f follow", "i ws", "+/- ctx", "W space", "H syntax", "/ search", "F filter", "? help", "q quit"}
	}

	right := strings.Join(labels, "  ") + " "

	// History mode builds its line once the hints are sized, because the subject
	// takes whatever room they leave it. The path and its counts are not
	// repeated: the file header at the top of the body already has them.
	if m.logMode {
		c := m.commit()
		head := " " + c.Short + "  "
		tail := fmt.Sprintf("  ·  commit %d/%d  ·  file %d/%d",
			m.commitIdx+1, len(m.commits), fileIdx+1, len(m.files))
		if m.width >= logAuthorWidth {
			tail += fmt.Sprintf("  ·  %s, %s", c.Author, c.Rel)
		}
		if m.panelFocus() == focusCommits && m.commitSearch != "" {
			tail += "  ·  /" + m.commitSearch
		} else if m.search != "" {
			tail += "  ·  /" + m.search
		}
		if m.ignoreWS {
			tail += "  ·  ≈ ws"
		}
		if m.context != diff.DefaultContext {
			tail += fmt.Sprintf("  ·  ⋯ %d", m.context)
		}
		room := m.width - lipgloss.Width(head+tail+right) - 1
		if room < 8 {
			room = m.width - lipgloss.Width(head+tail) - 1 // no room for the hints anyway
		}
		left = head + clip(c.Subject, min(max(room, 8), 64)) + tail
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return fit(m.st.statusbar.Render(left), 0, m.width, m.st.statusbar)
	}

	// Record where each label lands so a click there runs its key. The right
	// block starts after the left text and the gap that pushes it to the edge.
	x := lipgloss.Width(left) + gap
	for i, label := range labels {
		opts[i].x0 = x
		opts[i].x1 = x + lipgloss.Width(label) - 1
		x += lipgloss.Width(label) + 2 // labels are joined by two spaces
	}
	m.hints = opts

	return m.st.statusbar.Render(left + strings.Repeat(" ", gap) + right)
}

func (m *Model) renderHelp() string {
	rows := [][2]string{
		{"j / k, ↑ / ↓", "scroll a line"},
		{"ctrl-d / ctrl-u", "scroll half a page"},
		{"n / p", "next / previous hunk"},
		{"] / [", "next / previous file"},
		{"g / G", "top / bottom"},
		{"/", "search; enter jumps, esc clears"},
		{"n / N", "next / previous match (while searching)"},
		{"W", "show / hide whitespace (tabs, trailing spaces)"},
		{"H", "toggle syntax highlighting"},
		{"F", "filter out hunks matching a regex; empty clears"},
		{"h / l, ← / →", "scroll sideways"},
		{"s", "toggle side-by-side / unified"},
		{"b", "toggle the file sidebar"},
		{"shift-← / →", "narrow / widen the sidebar"},
		{"ctrl-w", "move focus to the sidebar and back; j / k follow it"},
		{"space, - / +", "on a sidebar folder: toggle, fold / unfold it"},
		{"?", "this help"},
		{"q", "quit"},
		{"", ""},
		{"mouse", "click a file, row, or status-bar option; wheel scrolls"},
	}
	if m.logMode {
		rows = append(rows,
			[2]string{"", ""},
			[2]string{"} / {", "older / newer commit"},
			[2]string{"ctrl-w", "focus the diff, then commits, then files"},
			[2]string{"/ then n / N", "search commits by subject or author (commits panel)"},
			[2]string{"i", "ignore / show whitespace-only changes"},
			[2]string{"+ / -", "more / less context around each hunk"},
		)
	}
	if m.staging() {
		rows = append(rows,
			[2]string{"", ""},
			[2]string{"space", "mark this hunk and move to the next one in the file"},
			[2]string{"a / d", "mark / unmark this file, or every file in a folder"},
			[2]string{"w", "stage what is marked"},
			[2]string{"u", "undo the last stage (skips a stuck entry)"},
			[2]string{"E", "edit this file at the cursor in $VISUAL / $EDITOR"},
			[2]string{"f", "pause / resume following file changes"},
			[2]string{"i", "ignore / show whitespace-only changes"},
			[2]string{"+ / -", "more / less context around each hunk"},
		)
	}

	lines := []string{m.st.modalTitle.Render("hunk — keys"), ""}
	for _, r := range rows {
		if r[0] == "" && r[1] == "" {
			lines = append(lines, m.st.base.Render(""))
			continue
		}
		lines = append(lines, m.st.help.Render(fmt.Sprintf("%-18s %s", r[0], r[1])))
	}
	lines = append(lines, "", m.st.notice.Render("press any key to close"))

	return m.st.modal.Render(strings.Join(lines, "\n"))
}
