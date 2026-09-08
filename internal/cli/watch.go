package cli

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"job/internal/db"
	"job/internal/model"
)

const (
	watchRefreshInterval = 500 * time.Millisecond
)

var (
	watchFilter        string
	watchContextFilter string
	watchSince         string
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Continuously watch jobs and their dependencies",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if watchFilter != "" {
			if _, err := regexp.Compile(watchFilter); err != nil {
				return fmt.Errorf("invalid filter: %w", err)
			}
		}
		if cmd.Flags().Changed("context") {
			if watchContextFilter == "" {
				return fmt.Errorf("context filter cannot be empty")
			}
			if _, err := regexp.Compile(watchContextFilter); err != nil {
				return fmt.Errorf("invalid context filter: %w", err)
			}
		}
		now := time.Now().UTC()
		completedSince, err := watchCompletedSince(now, watchSince)
		if err != nil {
			return err
		}
		if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
			return fmt.Errorf("watch requires an interactive terminal")
		}

		resolveCtx()
		context := hereCtx
		if anyScope {
			context = ""
		}
		query := watchQuery{
			filter:         watchFilter,
			context:        context,
			contextRegex:   cmd.Flags().Changed("context"),
			completedSince: completedSince,
		}
		if query.contextRegex {
			query.context = watchContextFilter
		}

		scope := watchScope(query, anyScope)
		if watchSince != "" {
			scope += " · since " + watchSince
		}
		m := newWatchModel(query.load(globalDB), scope, now)
		_, err = tea.NewProgram(m).Run()
		return err
	},
}

func init() {
	watchCmd.Flags().StringVarP(&watchFilter, "filter", "f", "", "filter by command regex")
	watchCmd.Flags().StringVar(&watchContextFilter, "context", "", "filter by context regex")
	watchCmd.Flags().StringVar(&watchSince, "since", "", "include jobs completed within duration before startup (e.g. 1h, 2d)")
	addAnyFlag(watchCmd)
	watchCmd.MarkFlagsMutuallyExclusive("any", "context")
	rootCmd.AddCommand(watchCmd)
}

type watchQuery struct {
	filter         string
	context        string
	contextRegex   bool
	completedSince time.Time
}

type watchSnapshot struct {
	jobs []*model.Job
}

func (q watchQuery) load(d *db.DB) func() (watchSnapshot, error) {
	completedSince := q.completedSince
	return func() (watchSnapshot, error) {
		// Advance from the start of a successful refresh. A job that completes
		// while the queries are running will therefore be included next time.
		refreshStarted := time.Now().UTC()
		listActive := d.ListActive
		listCompleted := d.ListCompletedSince
		if q.contextRegex {
			listActive = d.ListActiveByContextRegex
			listCompleted = d.ListCompletedSinceByContextRegex
		}

		active, err := listActive(q.filter, q.context)
		if err != nil {
			return watchSnapshot{}, err
		}
		completed, err := listCompleted(completedSince, q.filter, q.context)
		if err != nil {
			return watchSnapshot{}, err
		}
		seeds := make([]*model.Job, 0, len(active)+len(completed))
		seeds = append(seeds, active...)
		seeds = append(seeds, completed...)
		closure, err := expandDeps(d, seeds)
		if err != nil {
			return watchSnapshot{}, err
		}

		byKey := make(map[string]*model.Job, len(completed)+len(closure))
		for _, j := range completed {
			byKey[j.Key] = j
		}
		for _, j := range closure {
			byKey[j.Key] = j
		}

		jobs := make([]*model.Job, 0, len(byKey))
		for _, j := range byKey {
			jobs = append(jobs, j)
		}
		sort.Slice(jobs, func(i, k int) bool {
			if jobs[i].CreatedAt.Equal(jobs[k].CreatedAt) {
				return jobs[i].Key < jobs[k].Key
			}
			return jobs[i].CreatedAt.Before(jobs[k].CreatedAt)
		})
		completedSince = refreshStarted
		return watchSnapshot{jobs: jobs}, nil
	}
}

func watchCompletedSince(now time.Time, since string) (time.Time, error) {
	if since == "" {
		return now, nil
	}
	d, err := parseDuration(since)
	if err != nil {
		return time.Time{}, fmt.Errorf("--since: %w", err)
	}
	if d < 0 {
		return time.Time{}, fmt.Errorf("--since cannot be negative")
	}
	return now.Add(-d), nil
}

func watchScope(q watchQuery, allContexts bool) string {
	switch {
	case q.contextRegex:
		return fmt.Sprintf("contexts matching %q", q.context)
	case allContexts:
		return "all contexts"
	default:
		return "context: " + displayContext(&model.Job{Context: q.context})
	}
}

type watchLoader func() (watchSnapshot, error)

type watchModel struct {
	loader   watchLoader
	scope    string
	observed map[string]*model.Job
	now      time.Time
	width    int
	height   int
	queryErr error
}

type watchRefreshMsg struct {
	snapshot watchSnapshot
	err      error
	at       time.Time
}

type watchTickMsg time.Time

func newWatchModel(loader watchLoader, scope string, now time.Time) *watchModel {
	return &watchModel{
		loader:   loader,
		scope:    scope,
		observed: make(map[string]*model.Job),
		now:      now,
		width:    80,
		height:   24,
	}
}

func (m *watchModel) Init() tea.Cmd {
	return m.loadCmd()
}

func (m *watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case watchTickMsg:
		m.now = time.Time(msg)
		return m, m.loadCmd()
	case watchRefreshMsg:
		m.now = msg.at
		m.queryErr = msg.err
		if msg.err == nil {
			m.applySnapshot(msg.snapshot)
		}
		return m, watchTickCmd()
	}
	return m, nil
}

func (m *watchModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "job watch"
	return v
}

func (m *watchModel) loadCmd() tea.Cmd {
	return func() tea.Msg {
		snapshot, err := m.loader()
		return watchRefreshMsg{snapshot: snapshot, err: err, at: time.Now()}
	}
}

func watchTickCmd() tea.Cmd {
	return tea.Tick(watchRefreshInterval, func(t time.Time) tea.Msg {
		return watchTickMsg(t)
	})
}

func (m *watchModel) applySnapshot(snapshot watchSnapshot) {
	for _, j := range snapshot.jobs {
		m.observed[j.Key] = j
	}
}

func (m *watchModel) render() string {
	width := max(m.width, 1)
	height := max(m.height, 2)

	header := lipgloss.NewStyle().Bold(true).Render(m.header())
	headerLines := []string{header}
	if m.queryErr != nil {
		headerLines = append(headerLines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("refresh failed: "+m.queryErr.Error()),
		)
	}

	jobs := m.jobs()
	body := jobForestLines(jobs, m.now)
	if len(body) == 0 {
		body = []string{"No matching jobs; waiting for new activity…"}
	}

	// Header + blank + body + blank + footer. On very short terminals, keep
	// the primary header and footer and spend all remaining rows on content.
	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("q / ctrl+c quit")
	fixedRows := len(headerLines) + 3
	bodyRows := max(height-fixedRows, 0)
	if len(body) > bodyRows {
		hidden := len(body)
		if bodyRows > 0 {
			visible := max(bodyRows-1, 0)
			hidden -= visible
			body = append(body[:visible], fmt.Sprintf("… %d jobs hidden", hidden))
		} else {
			body = nil
		}
	}

	lines := append([]string{}, headerLines...)
	if len(lines)+1+len(body)+1+1 <= height {
		lines = append(lines, "")
	}
	lines = append(lines, body...)
	if len(lines)+2 <= height {
		lines = append(lines, "")
	}
	if len(lines) < height {
		lines = append(lines, footer)
	} else if height >= 2 {
		lines[height-1] = footer
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	truncateTerminalLines(lines, width)
	return strings.Join(lines, "\n")
}

func (m *watchModel) header() string {
	counts := make(map[string]int)
	for _, j := range m.observed {
		counts[jobStatusText(j)]++
	}
	parts := []string{"job watch", m.scope}
	for _, status := range []string{"pending", "blocked", "running", "succeeded", "failed", "stopped", "skipped"} {
		if counts[status] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[status], status))
		}
	}
	if len(m.observed) == 0 {
		parts = append(parts, "idle")
	}
	return strings.Join(parts, " · ")
}

func (m *watchModel) jobs() []*model.Job {
	jobs := make([]*model.Job, 0, len(m.observed))
	for _, j := range m.observed {
		jobs = append(jobs, j)
	}
	return jobs
}
