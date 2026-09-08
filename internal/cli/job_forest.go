package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"

	"job/internal/db"
	"job/internal/model"
)

var jobForestContextStyle = lipgloss.NewStyle().Faint(true)

// expandDeps augments a set of jobs with their transitive completed dependencies.
func expandDeps(d *db.DB, seed []*model.Job) ([]*model.Job, error) {
	byKey := make(map[string]*model.Job, len(seed))
	for _, j := range seed {
		byKey[j.Key] = j
	}

	var pending []string
	for _, j := range seed {
		for _, dep := range j.Deps {
			if _, seen := byKey[dep.Key]; !seen {
				byKey[dep.Key] = nil
				pending = append(pending, dep.Key)
			}
		}
	}

	for len(pending) > 0 {
		fetched, err := d.GetByKeys(pending)
		if err != nil {
			return nil, err
		}
		pending = pending[:0]
		for _, j := range fetched {
			byKey[j.Key] = j
			for _, dep := range j.Deps {
				if _, seen := byKey[dep.Key]; !seen {
					byKey[dep.Key] = nil
					pending = append(pending, dep.Key)
				}
			}
		}
	}

	result := make([]*model.Job, 0, len(byKey))
	for _, j := range byKey {
		if j != nil {
			result = append(result, j)
		}
	}
	return result, nil
}

type jobForestNode struct {
	job        *model.Job
	parentKind model.DepKind
	extraDeps  []model.Dep
	children   []*jobForestNode
}

// jobForestLines renders jobs and their visible dependencies as a stable forest.
func jobForestLines(jobs []*model.Job, now time.Time) []string {
	byKey := make(map[string]*model.Job, len(jobs))
	nodes := make(map[string]*jobForestNode, len(jobs))
	for _, j := range jobs {
		byKey[j.Key] = j
		nodes[j.Key] = &jobForestNode{job: j}
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

	var roots []*jobForestNode
	for _, node := range nodes {
		if !parented[node.job.Key] {
			roots = append(roots, node)
		}
	}
	sortJobForestRoots(roots)
	for _, node := range nodes {
		sort.Slice(node.children, func(i, j int) bool {
			a, b := node.children[i].job, node.children[j].job
			if jobForestPriority(a) != jobForestPriority(b) {
				return jobForestPriority(a) < jobForestPriority(b)
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
	var renderNode func(*jobForestNode, string, bool, bool)
	renderNode = func(node *jobForestNode, prefix string, last, root bool) {
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
		lines = append(lines, linePrefix+jobForestNodeLabel(node, byKey, showContext, now))
		for i, child := range node.children {
			renderNode(child, childPrefix, i == len(node.children)-1, false)
		}
	}
	for _, root := range roots {
		renderNode(root, "", true, true)
	}

	// Corrupt or legacy cyclic dependency data should not make jobs vanish.
	// Render any nodes left without a root once, in stable key order.
	var remaining []*jobForestNode
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

func sortJobForestRoots(roots []*jobForestNode) {
	sort.Slice(roots, func(i, j int) bool {
		pi, ti := jobForestTreePriority(roots[i])
		pj, tj := jobForestTreePriority(roots[j])
		if pi != pj {
			return pi < pj
		}
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return roots[i].job.Key < roots[j].job.Key
	})
}

func jobForestTreePriority(node *jobForestNode) (int, time.Time) {
	priority := jobForestPriority(node.job)
	latest := jobForestTime(node.job)
	for _, child := range node.children {
		childPriority, childLatest := jobForestTreePriority(child)
		priority = min(priority, childPriority)
		if childLatest.After(latest) {
			latest = childLatest
		}
	}
	return priority, latest
}

func jobForestPriority(j *model.Job) int {
	if j.Status != model.StatusCompleted {
		return 0
	}
	return 1
}

func jobForestTime(j *model.Job) time.Time {
	if j.StoppedAt != nil {
		return *j.StoppedAt
	}
	if j.StartedAt != nil {
		return *j.StartedAt
	}
	return j.CreatedAt
}

func jobForestNodeLabel(node *jobForestNode, byKey map[string]*model.Job, showContext bool, now time.Time) string {
	j := node.job
	status := jobStatusStyle(j).Render(jobForestStatusGlyph(j) + " " + jobForestNodeStatusText(node))
	parts := []string{status, displayKeyAlias(j)}
	if showContext {
		parts = append(parts, jobForestContextStyle.Render("["+displayContext(j)+"]"))
	}
	if duration := jobForestDuration(j, now); duration != "" {
		parts = append(parts, duration)
	}
	if j.ExitCode != nil && j.Status == model.StatusCompleted {
		parts = append(parts, fmt.Sprintf("rc=%d", *j.ExitCode))
	}
	parts = append(parts, jobForestCommandText(j.Command))

	var details []string
	for _, dep := range node.extraDeps {
		details = append(details, "+ "+jobForestDepName(dep.Key, byKey))
	}
	if j.Status == model.StatusBlocked {
		for _, dep := range j.Deps {
			if byKey[dep.Key] == nil {
				details = append(details, "? "+dep.Key)
			}
		}
	}
	if len(details) > 0 {
		parts = append(parts, "("+strings.Join(details, ", ")+")")
	}
	return strings.Join(parts, "  ")
}

func jobForestNodeStatusText(node *jobForestNode) string {
	if node.job.Status != model.StatusBlocked {
		return jobStatusText(node.job)
	}
	kind := node.parentKind
	if kind == "" && len(node.job.Deps) > 0 {
		kind = node.job.Deps[0].Kind
	}
	if kind == model.DepAfterSuccess {
		return "after-ok"
	}
	if kind != "" {
		return strings.ReplaceAll(string(kind), "_", "-")
	}
	return jobStatusText(node.job)
}

func jobForestCommandText(command []string) string {
	args := make([]string, len(command))
	for i, arg := range command {
		args[i] = sanitizeTerminalText(arg)
	}
	return strings.Join(args, " ")
}

func sanitizeTerminalText(s string) string {
	if strings.IndexFunc(s, unicode.IsControl) == -1 {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unicode.IsControl(r) {
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			fmt.Fprintf(&b, `\x%02x`, r)
		}
	}
	return b.String()
}

func jobForestDepName(key string, byKey map[string]*model.Job) string {
	if j := byKey[key]; j != nil {
		return displayKeyAlias(j)
	}
	return key
}

func jobForestDuration(j *model.Job, now time.Time) string {
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

func jobForestStatusGlyph(j *model.Job) string {
	switch jobStatusText(j) {
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
	case "skipped":
		return "⊘"
	default:
		return "✗"
	}
}
