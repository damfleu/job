package core

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"job/internal/model"
	"job/internal/sequence"
)

func TestCaptureSequenceRoundTrip(t *testing.T) {
	a := &model.Job{Key: "a", Alias: "step-2", Command: []string{"printf", "%s", "", "$HOME"}, WorkDir: "/project"}
	b := &model.Job{Key: "b", Alias: "build", Command: []string{"make", "build"}, WorkDir: "/project", Deps: []model.Dep{{Key: "a", Kind: model.DepAfterSuccess}}}
	c := &model.Job{Key: "c", Command: []string{"make", "report"}, WorkDir: "/project", Deps: []model.Dep{{Key: "a", Kind: model.DepAfter}}}
	store := &fakeStore{jobs: []*model.Job{a, b, c}}
	d, err := CaptureSequence(store, []*model.Job{c, b, b})
	require.NoError(t, err)
	require.Len(t, d.Steps, 3)
	require.Equal(t, "step-1", d.Steps[0].ID)
	require.Equal(t, "step-2", d.Steps[1].ID)
	require.Equal(t, "step-3", d.Steps[2].ID)
	require.Equal(t, []string{"step-1"}, d.Steps[1].AfterSuccess)
	require.Equal(t, []string{"step-1"}, d.Steps[2].After)
	require.Equal(t, new("/project"), d.CWD)
	for _, step := range d.Steps {
		require.Nil(t, step.CWD)
	}
	var encoded bytes.Buffer
	require.NoError(t, sequence.Encode(&encoded, d))
	decoded, err := sequence.Decode(&encoded)
	require.NoError(t, err)
	plan, err := PlanSequence(decoded, "/invocation", "")
	require.NoError(t, err)
	for i, source := range []*model.Job{a, b, c} {
		require.Equal(t, source.Command, plan[i].Command)
		require.Equal(t, source.WorkDir, plan[i].WorkDir)
		require.True(t, plan[i].Automated, "manual source jobs still replay as automated")
	}
	again, err := CaptureSequence(store, []*model.Job{b, c})
	require.NoError(t, err)
	require.Equal(t, d, again, "capture must not depend on argument order")
	plan[0].Command[0] = "changed"
	require.Equal(t, "printf", a.Command[0])
	require.Equal(t, "printf", decoded.Steps[0].Command[0])
}

func TestCaptureSequenceMixedDirectories(t *testing.T) {
	a := &model.Job{Key: "a", Command: []string{"true"}, WorkDir: "/a"}
	b := &model.Job{Key: "b", Command: []string{"true"}, WorkDir: "/b"}
	d, err := CaptureSequence(&fakeStore{}, []*model.Job{a, b})
	require.NoError(t, err)
	require.Nil(t, d.CWD)
	require.Equal(t, new("/a"), d.Steps[0].CWD)
	require.Equal(t, new("/b"), d.Steps[1].CWD)
}

func TestCaptureSequenceRejectsIncompleteGraph(t *testing.T) {
	a := &model.Job{Key: "a", Command: []string{"true"}, WorkDir: "/a", Deps: []model.Dep{{Key: "missing", Kind: model.DepAfter}}}
	_, err := CaptureSequence(&fakeStore{}, []*model.Job{a})
	require.ErrorContains(t, err, `fetching dependency "missing"`)
	_, err = CaptureSequence(&fakeStore{}, nil)
	require.ErrorContains(t, err, "sequence has no steps")
	a.Deps = []model.Dep{{Key: "a", Kind: model.DepAfter}}
	_, err = CaptureSequence(&fakeStore{}, []*model.Job{a})
	require.ErrorContains(t, err, "cycle")
}

func TestPlanSequenceResolvesSettingsAndForwardReferences(t *testing.T) {
	d := &sequence.Definition{Version: 1, CWD: new("/base"), Automated: new(false), Steps: []sequence.Step{
		{ID: "report", Run: new("printf report > report.txt"), After: []string{"build"}, AfterSuccess: []string{"lint"}, Automated: new(true)},
		{ID: "build", Command: []string{"make", "build"}, CWD: new("/build")},
		{ID: "lint", Command: []string{"make", "lint"}},
	}}
	plan, err := PlanSequence(d, "/invocation", "")
	require.NoError(t, err)
	require.Equal(t, []string{"build", "lint", "report"}, []string{plan[0].ID, plan[1].ID, plan[2].ID})
	require.Equal(t, "/build", plan[0].WorkDir)
	require.Equal(t, "/base", plan[1].WorkDir)
	require.False(t, plan[0].Automated)
	require.False(t, plan[1].Automated)
	require.True(t, plan[2].Automated)
	require.Equal(t, []string{"sh", "-c", "printf report > report.txt"}, plan[2].Command)
	require.Equal(t, []model.Dep{{Key: "build", Kind: model.DepAfter}, {Key: "lint", Kind: model.DepAfterSuccess}}, plan[2].Deps)

	plan, err = PlanSequence(d, "/invocation", "/override")
	require.NoError(t, err)
	for _, step := range plan {
		require.Equal(t, "/override", step.WorkDir)
	}
	require.Equal(t, new("/base"), d.CWD)
	require.Equal(t, new("/build"), d.Steps[1].CWD)
}

func TestPlanSequenceDefaultsAndExplicitFalse(t *testing.T) {
	d := &sequence.Definition{Version: 1, Steps: []sequence.Step{
		{ID: "default", Command: []string{"true"}},
		{ID: "manual", Command: []string{"true"}, Automated: new(false)},
	}}
	plan, err := PlanSequence(d, "/invocation", "")
	require.NoError(t, err)
	require.True(t, plan[0].Automated)
	require.False(t, plan[1].Automated)
	for _, step := range plan {
		require.Equal(t, "/invocation", step.WorkDir)
	}
	d.Steps[1].After = []string{"missing"}
	plan, err = PlanSequence(d, "/invocation", "")
	require.ErrorContains(t, err, "unknown dependency")
	require.Nil(t, plan)
}
