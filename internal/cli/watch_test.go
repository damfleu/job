package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

func watchTestJob(key, alias string, status model.Status, created time.Time) *model.Job {
	j := &model.Job{
		Key:       key,
		Alias:     alias,
		Command:   []string{"echo", key},
		WorkDir:   "/tmp",
		LogFile:   "/tmp/" + key + ".log",
		Status:    status,
		Context:   "project-a",
		CreatedAt: created,
	}
	if status == model.StatusRunning {
		j.StartedAt = new(created.Add(time.Second))
	}
	return j
}

func completeWatchJob(j *model.Job, rc int, stopped time.Time) *model.Job {
	j.Status = model.StatusCompleted
	j.Reason = model.ReasonExited
	j.ExitCode = new(rc)
	j.StoppedAt = new(stopped)
	if j.StartedAt == nil {
		j.StartedAt = new(j.CreatedAt)
	}
	return j
}

func TestWatchRetainsAllObservedJobs(t *testing.T) {
	now := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	m := newWatchModel(func() (watchSnapshot, error) { return watchSnapshot{}, nil }, "all contexts", now)

	running := watchTestJob("running", "run", model.StatusRunning, now.Add(-time.Minute))
	recent := completeWatchJob(watchTestJob("recent", "recent", model.StatusPending, now.Add(-time.Hour)), 0, now.Add(-4*time.Minute))
	expired := completeWatchJob(watchTestJob("expired", "expired", model.StatusPending, now.Add(-time.Hour)), 0, now.Add(-6*time.Minute))
	required := completeWatchJob(watchTestJob("required", "required", model.StatusPending, now.Add(-time.Hour)), 0, now.Add(-10*time.Minute))
	failed := completeWatchJob(watchTestJob("failed", "failed", model.StatusPending, now.Add(-time.Hour)), 1, now.Add(-20*time.Minute))

	m.applySnapshot(watchSnapshot{jobs: []*model.Job{running, recent, expired, required, failed}})
	assert.ElementsMatch(t, []string{"running", "recent", "expired", "required", "failed"}, watchObservedKeys(m))

	m.applySnapshot(watchSnapshot{})
	assert.ElementsMatch(t, []string{"running", "recent", "expired", "required", "failed"}, watchObservedKeys(m))

	finished := completeWatchJob(watchTestJob("running", "run", model.StatusPending, now.Add(-time.Minute)), 0, now)
	m.applySnapshot(watchSnapshot{jobs: []*model.Job{finished}})
	assert.Equal(t, model.StatusCompleted, m.observed["running"].Status)
}

func TestWatchDepFailedAndStoppedAreSticky(t *testing.T) {
	now := time.Now().UTC()
	m := newWatchModel(func() (watchSnapshot, error) { return watchSnapshot{}, nil }, "scope", now)
	depFailed := watchTestJob("dep", "dep", model.StatusCompleted, now.Add(-time.Hour))
	depFailed.Reason = model.ReasonDepFailed
	depFailed.StoppedAt = new(now.Add(-time.Hour))
	stopped := watchTestJob("stopped", "stopped", model.StatusCompleted, now.Add(-time.Hour))
	stopped.Reason = model.ReasonStopped
	stopped.StoppedAt = new(now.Add(-time.Hour))

	m.applySnapshot(watchSnapshot{jobs: []*model.Job{depFailed, stopped}})
	assert.ElementsMatch(t, []string{"dep", "stopped"}, watchObservedKeys(m))
	assert.Contains(t, m.header(), "1 skipped")
	assert.NotContains(t, m.header(), "dep-failed")
}

func TestJobForestUsesHumanOutcomeLabels(t *testing.T) {
	now := time.Now().UTC()
	succeeded := completeWatchJob(watchTestJob("success-key", "success", model.StatusPending, now), 0, now)
	skipped := watchTestJob("skipped-key", "skipped", model.StatusCompleted, now)
	skipped.Reason = model.ReasonDepFailed
	skipped.StoppedAt = new(now)

	output := strings.Join(jobForestLines([]*model.Job{succeeded, skipped}, now), "\n")
	assert.Contains(t, output, "✓ succeeded")
	assert.Contains(t, output, "⊘ skipped")
	assert.NotContains(t, output, "dep-failed")
}

func TestWatchRefreshErrorPreservesLastSnapshot(t *testing.T) {
	now := time.Now().UTC()
	m := newWatchModel(func() (watchSnapshot, error) { return watchSnapshot{}, nil }, "scope", now)
	running := watchTestJob("running", "run", model.StatusRunning, now)
	m.applySnapshot(watchSnapshot{jobs: []*model.Job{running}})

	updated, cmd := m.Update(watchRefreshMsg{err: errors.New("database busy"), at: now.Add(time.Second)})
	got := updated.(*watchModel)
	require.NotNil(t, cmd)
	assert.Contains(t, got.queryErr.Error(), "database busy")
	assert.Contains(t, got.observed, "running")
	assert.Contains(t, got.render(), "refresh failed: database busy")
}

func TestWatchCompletedSince(t *testing.T) {
	now := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)

	got, err := watchCompletedSince(now, "")
	require.NoError(t, err)
	assert.Equal(t, now, got)

	got, err = watchCompletedSince(now, "1h")
	require.NoError(t, err)
	assert.Equal(t, now.Add(-time.Hour), got)

	got, err = watchCompletedSince(now, "2d")
	require.NoError(t, err)
	assert.Equal(t, now.Add(-48*time.Hour), got)

	_, err = watchCompletedSince(now, "later")
	assert.EqualError(t, err, `--since: invalid duration "later"`)

	_, err = watchCompletedSince(now, "-1h")
	assert.EqualError(t, err, "--since cannot be negative")
}

func TestWatchQueryCatchesFastCompletionAndExpandsActiveDeps(t *testing.T) {
	d := openTestDB(t)
	start := time.Now().UTC().Add(-time.Second)

	oldDep := completeWatchJob(watchTestJob("old-dep", "build", model.StatusPending, start.Add(-time.Hour)), 0, start.Add(-time.Minute))
	oldDep.Context = "other-context"
	active := watchTestJob("active", "test", model.StatusBlocked, start)
	active.Deps = []model.Dep{{Key: oldDep.Key, Kind: model.DepAfterSuccess}}
	fast := completeWatchJob(watchTestJob("fast", "lint", model.StatusPending, start), 0, start.Add(500*time.Millisecond))
	historyDep := completeWatchJob(watchTestJob("history-dep", "history-dep", model.StatusPending, start.Add(-time.Hour)), 0, start.Add(-time.Minute))
	historyDep.Context = "other-context"
	historyChild := completeWatchJob(watchTestJob("history-child", "history-child", model.StatusPending, start), 0, start.Add(600*time.Millisecond))
	historyChild.Deps = []model.Dep{{Key: historyDep.Key, Kind: model.DepAfterSuccess}}
	for _, j := range []*model.Job{oldDep, active, fast, historyDep, historyChild} {
		require.NoError(t, d.Insert(j))
	}

	loader := (watchQuery{context: "project-a", completedSince: start}).load(d)
	snapshot, err := loader()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"old-dep", "active", "fast", "history-dep", "history-child"}, watchSnapshotKeys(snapshot))

	// The next refresh queries only completions since the prior refresh began.
	// A newly inserted historical completion must not be returned again.
	historical := completeWatchJob(watchTestJob("historical", "historical", model.StatusPending, start), 0, start.Add(750*time.Millisecond))
	require.NoError(t, d.Insert(historical))
	time.Sleep(5 * time.Millisecond)
	newCompletion := completeWatchJob(watchTestJob("new", "new", model.StatusPending, time.Now().UTC()), 0, time.Now().UTC())
	require.NoError(t, d.Insert(newCompletion))
	snapshot, err = loader()
	require.NoError(t, err)
	assert.NotContains(t, watchSnapshotKeys(snapshot), "historical")
	assert.Contains(t, watchSnapshotKeys(snapshot), "new")
}

func TestWatchForestRendersDependenciesOnceAndDeterministically(t *testing.T) {
	now := time.Now().UTC()
	build := completeWatchJob(watchTestJob("build-key", "build", model.StatusPending, now.Add(-time.Minute)), 0, now.Add(-30*time.Second))
	lint := watchTestJob("lint-key", "lint", model.StatusRunning, now.Add(-20*time.Second))
	testJob := watchTestJob("test-key", "test", model.StatusBlocked, now.Add(-10*time.Second))
	testJob.Deps = []model.Dep{
		{Key: build.Key, Kind: model.DepAfterSuccess},
		{Key: lint.Key, Kind: model.DepAfter},
	}

	jobs := []*model.Job{testJob, lint, build}
	first := strings.Join(jobForestLines(jobs, now), "\n")
	second := strings.Join(jobForestLines(jobs, now), "\n")
	assert.Equal(t, first, second)
	assert.Equal(t, 1, strings.Count(first, "echo test-key"))
	assert.Contains(t, first, "└──")
	assert.Contains(t, first, "◌ after-ok")
	assert.Contains(t, first, "(+ lint)")
	assert.NotContains(t, first, "waiting for")
}

func TestJobForestMarksMissingDependenciesCompactly(t *testing.T) {
	now := time.Now().UTC()
	j := watchTestJob("blocked-key", "blocked", model.StatusBlocked, now)
	j.Deps = []model.Dep{{Key: "missing", Kind: model.DepAfterSuccess}}

	output := strings.Join(jobForestLines([]*model.Job{j}, now), "\n")
	assert.Contains(t, output, "◌ after-ok")
	assert.Contains(t, output, "(? missing)")
	assert.NotContains(t, output, "waiting for")
}

func TestJobForestUsesDependencyKindWhileBlocked(t *testing.T) {
	now := time.Now().UTC()
	for _, tt := range []struct {
		name string
		kind model.DepKind
		want string
	}{
		{name: "after", kind: model.DepAfter, want: "◌ after"},
		{name: "after success", kind: model.DepAfterSuccess, want: "◌ after-ok"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parent := watchTestJob("parent-key", "parent", model.StatusRunning, now)
			child := watchTestJob("child-key", "child", model.StatusBlocked, now)
			child.Deps = []model.Dep{{Key: parent.Key, Kind: tt.kind}}

			output := strings.Join(jobForestLines([]*model.Job{parent, child}, now), "\n")
			assert.Contains(t, output, tt.want)
			assert.NotContains(t, output, "◌ blocked")
		})
	}
}

func TestWatchForestSanitizesCommandControlCharacters(t *testing.T) {
	now := time.Now().UTC()
	j := watchTestJob("unsafe-key", "unsafe", model.StatusRunning, now)
	j.Command = []string{
		"printf",
		"first\nsecond",
		"tab\tvalue",
		"\x1b[31mred\x1b[0m",
		"bell\a",
		`plain\path`,
	}

	lines := jobForestLines([]*model.Job{j}, now)
	require.Len(t, lines, 1)
	line := lines[0]
	assert.Contains(t, line, `first\nsecond`)
	assert.Contains(t, line, `tab\tvalue`)
	assert.Contains(t, line, `\x1b[31mred\x1b[0m`)
	assert.Contains(t, line, `bell\x07`)
	assert.Contains(t, line, `plain\path`)
	assert.NotContains(t, line, "\n")
	assert.NotContains(t, line, "\t")
	assert.NotContains(t, line, "\x1b[31mred\x1b[0m")
	assert.NotContains(t, line, "\a")
}

func TestWatchForestPrioritizesActiveThenSortsCompletedByLatestActivity(t *testing.T) {
	now := time.Now().UTC()
	running := watchTestJob("active-key", "active", model.StatusRunning, now.Add(-time.Minute))
	failed := completeWatchJob(watchTestJob("failed-key", "failed", model.StatusPending, now.Add(-time.Minute)), 1, now.Add(-30*time.Second))
	success := completeWatchJob(watchTestJob("success-key", "success", model.StatusPending, now.Add(-time.Minute)), 0, now)

	output := strings.Join(jobForestLines([]*model.Job{success, failed, running}, now), "\n")
	assert.Less(t, strings.Index(output, "echo active-key"), strings.Index(output, "echo success-key"))
	assert.Less(t, strings.Index(output, "echo success-key"), strings.Index(output, "echo failed-key"))
}

func TestWatchRenderFitsTerminalAndReportsHiddenJobs(t *testing.T) {
	now := time.Now().UTC()
	m := newWatchModel(func() (watchSnapshot, error) { return watchSnapshot{}, nil }, "all contexts", now)
	m.width = 38
	m.height = 7
	for i := range 8 {
		key := strings.Repeat("long", 4) + string(rune('a'+i))
		m.observed[key] = watchTestJob(key, "", model.StatusRunning, now)
	}

	output := m.render()
	lines := strings.Split(output, "\n")
	assert.LessOrEqual(t, len(lines), m.height)
	assert.Contains(t, output, "jobs hidden")
	assert.Contains(t, output, "q / ctrl+c quit")
	for _, line := range lines {
		assert.LessOrEqual(t, lipgloss.Width(line), m.width)
	}
}

func TestWatchIdleAndMixedContexts(t *testing.T) {
	now := time.Now().UTC()
	m := newWatchModel(func() (watchSnapshot, error) { return watchSnapshot{}, nil }, "all contexts", now)
	assert.Contains(t, m.render(), "idle")
	assert.Contains(t, m.render(), "waiting for new activity")

	a := watchTestJob("a", "a", model.StatusRunning, now)
	b := watchTestJob("b", "b", model.StatusRunning, now)
	b.Context = "project-b"
	output := strings.Join(jobForestLines([]*model.Job{a, b}, now), "\n")
	assert.Contains(t, output, jobForestContextStyle.Render("[project-a]"))
	assert.Contains(t, output, jobForestContextStyle.Render("[project-b]"))
}

func TestWatchQuitKeysAndResize(t *testing.T) {
	m := newWatchModel(func() (watchSnapshot, error) { return watchSnapshot{}, nil }, "scope", time.Now())
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	assert.Equal(t, 100, updated.(*watchModel).width)
	assert.Nil(t, cmd)

	for _, key := range []tea.Key{
		{Text: "q", Code: 'q'},
		{Code: 'c', Mod: tea.ModCtrl},
	} {
		_, cmd = m.Update(tea.KeyPressMsg(key))
		require.NotNil(t, cmd)
		_, ok := cmd().(tea.QuitMsg)
		assert.True(t, ok)
	}
}

func watchObservedKeys(m *watchModel) []string {
	keys := make([]string, 0, len(m.observed))
	for key := range m.observed {
		keys = append(keys, key)
	}
	return keys
}

func watchSnapshotKeys(snapshot watchSnapshot) []string {
	keys := make([]string, len(snapshot.jobs))
	for i, j := range snapshot.jobs {
		keys[i] = j.Key
	}
	return keys
}
