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

var watchContextStyle = lipgloss.NewStyle().Faint(true)

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

func watchSucceeded(j *model.Job) bool {
	return j.Status == model.StatusCompleted &&
		j.Reason == model.ReasonExited &&
		j.ExitCode != nil && *j.ExitCode == 0
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
	body := watchForestLines(jobs, m.now)
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
	for i := range lines {
		lines[i] = watchTruncate(lines[i], width)
	}
	return strings.Join(lines, "\n")
}

func (m *watchModel) header() string {
	counts := make(map[string]int)
	for _, j := range m.observed {
		counts[watchStatusText(j)]++
	}
	parts := []string{"job watch", m.scope}
	for _, status := range []string{"pending", "blocked", "running", "succeeded", "failed", "stopped", "dep-failed"} {
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

func watchTruncate(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	return lipgloss.NewStyle().MaxWidth(width-1).Render(s) + "…"
}

type watchTreeNode struct {
	job        *model.Job
	parentKind model.DepKind
	extraDeps  []model.Dep
	children   []*watchTreeNode
}

func watchForestLines(jobs []*model.Job, now time.Time) []string {
	byKey := make(map[string]*model.Job, len(jobs))
	nodes := make(map[string]*watchTreeNode, len(jobs))
	for _, j := range jobs {
		byKey[j.Key] = j
		nodes[j.Key] = &watchTreeNode{job: j}
	}

	parented := make(map[string]bool)
	for _, j := range jobs {
		var visible []model.Dep
		for _, dep := range j.Deps {
			if byKey[dep.Key] != nil {
				visible = append(visible, dep)
			}
		}
		if len(visible) == 0 {
			continue
		}
		node := nodes[j.Key]
		node.parentKind = visible[0].Kind
		node.extraDeps = visible[1:]
		nodes[visible[0].Key].children = append(nodes[visible[0].Key].children, node)
		parented[j.Key] = true
	}

	var roots []*watchTreeNode
	for _, node := range nodes {
		if !parented[node.job.Key] {
			roots = append(roots, node)
		}
	}
	sortWatchRoots(roots)
	for _, node := range nodes {
		sort.Slice(node.children, func(i, j int) bool {
			a, b := node.children[i].job, node.children[j].job
			if watchJobPriority(a) != watchJobPriority(b) {
				return watchJobPriority(a) < watchJobPriority(b)
			}
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.Before(b.CreatedAt)
			}
			return a.Key < b.Key
		})
	}

	showContext := hasMultipleContexts(jobs)
	visited := make(map[string]bool, len(jobs))
	var lines []string
	var renderNode func(*watchTreeNode, string, bool, bool)
	renderNode = func(node *watchTreeNode, prefix string, last, root bool) {
		if visited[node.job.Key] {
			return
		}
		visited[node.job.Key] = true
		linePrefix := prefix
		childPrefix := prefix
		if !root {
			if last {
				linePrefix += "└── "
				childPrefix += "    "
			} else {
				linePrefix += "├── "
				childPrefix += "│   "
			}
		}
		lines = append(lines, linePrefix+watchNodeLabel(node, byKey, showContext, now))
		for i, child := range node.children {
			renderNode(child, childPrefix, i == len(node.children)-1, false)
		}
	}
	for _, root := range roots {
		renderNode(root, "", true, true)
	}
	// Corrupt or legacy cyclic dependency data should not make jobs vanish.
	// Render any nodes left without a root once, in stable key order.
	var remaining []*watchTreeNode
	for _, node := range nodes {
		if !visited[node.job.Key] {
			remaining = append(remaining, node)
		}
	}
	sort.Slice(remaining, func(i, j int) bool { return remaining[i].job.Key < remaining[j].job.Key })
	for _, node := range remaining {
		renderNode(node, "", true, true)
	}
	return lines
}

func sortWatchRoots(roots []*watchTreeNode) {
	sort.Slice(roots, func(i, j int) bool {
		pi, ti := watchTreePriority(roots[i])
		pj, tj := watchTreePriority(roots[j])
		if pi != pj {
			return pi < pj
		}
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return roots[i].job.Key < roots[j].job.Key
	})
}

func watchTreePriority(node *watchTreeNode) (int, time.Time) {
	priority := watchJobPriority(node.job)
	latest := watchJobTime(node.job)
	for _, child := range node.children {
		childPriority, childLatest := watchTreePriority(child)
		priority = min(priority, childPriority)
		if childLatest.After(latest) {
			latest = childLatest
		}
	}
	return priority, latest
}

func watchJobPriority(j *model.Job) int {
	if j.Status != model.StatusCompleted {
		return 0
	}
	return 1
}

func watchJobTime(j *model.Job) time.Time {
	if j.StoppedAt != nil {
		return *j.StoppedAt
	}
	if j.StartedAt != nil {
		return *j.StartedAt
	}
	return j.CreatedAt
}

func watchNodeLabel(node *watchTreeNode, byKey map[string]*model.Job, showContext bool, now time.Time) string {
	j := node.job
	status := jobStatusStyle(j).Render(watchStatusGlyph(j) + " " + watchStatusText(j))
	parts := []string{status, displayKeyAlias(j)}
	if showContext {
		parts = append(parts, watchContextStyle.Render("["+displayContext(j)+"]"))
	}
	if duration := watchDuration(j, now); duration != "" {
		parts = append(parts, duration)
	}
	if j.ExitCode != nil && j.Status == model.StatusCompleted {
		parts = append(parts, fmt.Sprintf("rc=%d", *j.ExitCode))
	}
	parts = append(parts, strings.Join(j.Command, " "))

	var details []string
	if node.parentKind != "" {
		details = append(details, strings.ReplaceAll(string(node.parentKind), "_", "-"))
	}
	for _, dep := range node.extraDeps {
		details = append(details, "also "+strings.ReplaceAll(string(dep.Kind), "_", "-")+" "+watchDepName(dep.Key, byKey))
	}
	if j.Status == model.StatusBlocked {
		var waiting []string
		for _, dep := range j.Deps {
			depJob := byKey[dep.Key]
			if depJob == nil || depJob.Status != model.StatusCompleted {
				waiting = append(waiting, watchDepName(dep.Key, byKey))
			}
		}
		if len(waiting) > 0 {
			details = append(details, "waiting for "+strings.Join(waiting, ", "))
		}
	}
	if len(details) > 0 {
		parts = append(parts, "("+strings.Join(details, "; ")+")")
	}
	return strings.Join(parts, "  ")
}

func watchDepName(key string, byKey map[string]*model.Job) string {
	if j := byKey[key]; j != nil {
		return displayKeyAlias(j)
	}
	return key
}

func watchDuration(j *model.Job, now time.Time) string {
	var start, end time.Time
	switch j.Status {
	case model.StatusPending, model.StatusBlocked:
		start, end = j.CreatedAt, now
	case model.StatusRunning:
		if j.StartedAt == nil {
			return ""
		}
		start, end = *j.StartedAt, now
	case model.StatusCompleted:
		if j.StoppedAt == nil {
			return ""
		}
		start, end = j.CreatedAt, *j.StoppedAt
		if j.StartedAt != nil {
			start = *j.StartedAt
		}
	default:
		return ""
	}
	if end.Before(start) {
		return "0s"
	}
	d := end.Sub(start)
	if d < time.Second {
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

func watchStatusText(j *model.Job) string {
	if j.Status != model.StatusCompleted {
		return string(j.Status)
	}
	switch j.Reason {
	case model.ReasonStopped:
		return "stopped"
	case model.ReasonDepFailed:
		return "dep-failed"
	case model.ReasonExited:
		if watchSucceeded(j) {
			return "succeeded"
		}
		return "failed"
	default:
		return "failed"
	}
}

func watchStatusGlyph(j *model.Job) string {
	switch watchStatusText(j) {
	case "pending":
		return "○"
	case "blocked":
		return "◌"
	case "running":
		return "●"
	case "succeeded":
		return "✓"
	case "stopped":
		return "■"
	case "dep-failed":
		return "⊘"
	default:
		return "✗"
	}
}
