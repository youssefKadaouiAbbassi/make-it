package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"makeit/internal/plan"
)

// LiveView draws a running plan in place: one row per area (Machine, Claude Code, Plugins, ...), a
// spinner on the running one, a bar where an area has several steps. It never reads the keyboard,
// so Ctrl-C stays an ordinary interrupt, and it hands the terminal back while a command such as
// sudo needs it.
type LiveView struct {
	prog *tea.Program
	term termHolder
	done chan struct{}
}

type termHolder interface {
	ReleaseTerminal() error
	RestoreTerminal() error
}

type eventMsg plan.Event
type stopMsg struct{ interrupted bool }

// NewLive starts the live view on u.Out with the areas of the planned steps, in plan order.
func NewLive(u *UI, planned []plan.Outcome) *LiveView {
	m := newLiveModel(u, planned)
	p := tea.NewProgram(m, tea.WithOutput(u.Out), tea.WithInput(nil), tea.WithoutSignalHandler())
	l := &LiveView{prog: p, term: p, done: make(chan struct{})}
	go func() {
		_, _ = p.Run()
		close(l.done)
	}()
	return l
}

// Event shows a step starting or finishing.
func (l *LiveView) Event(e plan.Event) { l.prog.Send(eventMsg(e)) }

// Hold gives the terminal back (a password prompt) until the returned func is called.
func (l *LiveView) Hold() (resume func()) {
	_ = l.term.ReleaseTerminal()
	return func() { _ = l.term.RestoreTerminal() }
}

// Stop draws the last frame, marking running areas interrupted when the run was, and waits for it.
func (l *LiveView) Stop(interrupted bool) {
	l.prog.Send(stopMsg{interrupted})
	<-l.done
}

type liveGroup struct {
	name            string
	total, finished int
	running         int
	seen, stopped   bool
	worst           int
	failDetail      string
}

type liveModel struct {
	u      *UI
	groups []*liveGroup
	spin   spinner.Model
	bar    progress.Model
	width  int
}

func newLiveModel(u *UI, planned []plan.Outcome) *liveModel {
	m := &liveModel{u: u}
	for _, o := range planned {
		m.group(Group(o.ID, o.Title)).total++
	}
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	if u.Color {
		sp.Style = u.accent()
	}
	m.spin = sp
	accent := lipgloss.Color("#4d8ef7")
	if u.Light {
		accent = lipgloss.Color("#1f6feb")
	}
	m.bar = progress.New(progress.WithWidth(24), progress.WithoutPercentage(), progress.WithFillCharacters('━', '─'), progress.WithColors(accent))
	return m
}

func (m *liveModel) group(name string) *liveGroup {
	for _, g := range m.groups {
		if g.name == name {
			return g
		}
	}
	g := &liveGroup{name: name}
	m.groups = append(m.groups, g)
	return g
}

// severity orders outcomes so the worst one decides an area's mark.
func severity(s plan.Status) int {
	switch s {
	case plan.Failed, plan.Interrupted:
		return 3
	case plan.Skipped:
		return 2
	case plan.Done:
		return 1
	}
	return 0
}

func (m *liveModel) apply(e plan.Event) {
	g := m.group(Group(e.Step.ID, e.Step.Title))
	g.seen = true
	if e.Outcome == nil {
		g.running++
		return
	}
	if g.running > 0 {
		g.running--
	}
	g.finished++
	if s := severity(e.Outcome.Status); s > g.worst {
		g.worst = s
		if s == 3 {
			g.failDetail = strings.TrimSpace(e.Outcome.Title + ": " + e.Outcome.Detail)
		}
	}
}

func (m *liveModel) stop(interrupted bool) {
	if !interrupted {
		return
	}
	for _, g := range m.groups {
		if g.running > 0 {
			g.stopped = true
		}
	}
}

func (m *liveModel) Init() tea.Cmd { return m.spin.Tick }

func (m *liveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case eventMsg:
		m.apply(plan.Event(msg))
	case stopMsg:
		m.stop(msg.interrupted)
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *liveModel) row(g *liveGroup) string {
	u := m.u
	switch {
	case g.stopped:
		return u.RowText(GFail, g.name, "", "interrupted")
	case !g.seen:
		return u.RowText(GPending, g.name, "", "")
	case g.running > 0 || (g.total > 0 && g.finished < g.total):
		s := "  " + m.spin.View() + " " + pad(g.name, LabelWidth)
		if g.total > 1 {
			s += m.bar.ViewAs(float64(g.finished)/float64(g.total)) + " " + u.style(faint, fmt.Sprintf("%d/%d", g.finished, g.total))
		}
		return s
	}
	switch g.worst {
	case 3:
		return u.RowText(GFail, g.name, g.failDetail, "")
	case 2:
		return u.RowText(GWarn, g.name, "", "skipped")
	case 0:
		return u.RowText(GDone, g.name, u.style(faint, "up to date"), "")
	}
	return u.RowText(GDone, g.name, "", "")
}

func (m *liveModel) View() tea.View {
	var b strings.Builder
	for _, g := range m.groups {
		r := m.row(g)
		if m.width > 0 {
			r = fit(r, m.width)
		}
		b.WriteString(r + "\n")
	}
	return tea.NewView(b.String())
}
