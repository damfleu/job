package core

import (
	"fmt"
	"path/filepath"
	"sort"

	"job/internal/db"
	"job/internal/model"
	"job/internal/sequence"
)

// captureJobs collects each source job and its transitive dependencies once.
func captureJobs(store db.JobStore, jobs []*model.Job) (map[string]*model.Job, error) {
	visited := make(map[string]*model.Job)
	queue := append([]*model.Job(nil), jobs...)
	for len(queue) > 0 {
		job := queue[0]
		queue = queue[1:]
		if job == nil {
			return nil, fmt.Errorf("sequence: cannot capture a nil job")
		}
		if _, exists := visited[job.Key]; exists {
			continue
		}
		visited[job.Key] = job
		for _, dep := range job.Deps {
			if _, exists := visited[dep.Key]; exists {
				continue
			}
			upstream, err := store.Get(dep.Key)
			if err != nil {
				return nil, fmt.Errorf("sequence: fetching dependency %q: %w", dep.Key, err)
			}
			queue = append(queue, upstream)
		}
	}
	return visited, nil
}

// CaptureSequence builds a portable definition without storing it. Job aliases
// are ignored; generated IDs follow deterministic topological order.
func CaptureSequence(store db.JobStore, jobs []*model.Job) (*sequence.Definition, error) {
	graph, err := captureJobs(store, jobs)
	if err != nil {
		return nil, err
	}
	order, err := topoSort(graph)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]string, len(order))
	for i, key := range order {
		ids[key] = fmt.Sprintf("step-%d", i+1)
	}
	d := &sequence.Definition{Version: sequence.FormatVersion, Automated: new(true)}
	for _, key := range order {
		job := graph[key]
		step := sequence.Step{
			ID: ids[key], Command: append([]string(nil), job.Command...), CWD: new(job.WorkDir),
		}
		for _, dep := range job.Deps {
			switch dep.Kind {
			case model.DepAfter:
				step.After = append(step.After, ids[dep.Key])
			case model.DepAfterSuccess:
				step.AfterSuccess = append(step.AfterSuccess, ids[dep.Key])
			default:
				return nil, fmt.Errorf("job %q: unknown dependency kind %q", key, dep.Kind)
			}
		}
		d.Steps = append(d.Steps, step)
	}
	if err := d.Validate(); err != nil {
		return nil, fmt.Errorf("captured sequence: %w", err)
	}
	// Preserve exact recorded directories; lift the common value only when all
	// steps share it. Mixed-directory captures keep explicit step directories.
	common := *d.Steps[0].CWD
	for _, step := range d.Steps[1:] {
		if *step.CWD != common {
			return d, nil
		}
	}
	d.CWD = new(common)
	for i := range d.Steps {
		d.Steps[i].CWD = nil
	}
	return d, nil
}

// PlanSequence validates and resolves a definition into independent execution
// descriptors. The caller supplies absolute invocation and override directories.
// No jobs are created and no working directories are accessed here.
func PlanSequence(d *sequence.Definition, invocationDir, workDirOverride string) ([]RunStep, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(invocationDir) {
		return nil, fmt.Errorf("invocation directory must be absolute")
	}
	if workDirOverride != "" && !filepath.IsAbs(workDirOverride) {
		return nil, fmt.Errorf("working directory override must be absolute")
	}
	baseDir := invocationDir
	if d.CWD != nil {
		baseDir = *d.CWD
	}
	automated := true
	if d.Automated != nil {
		automated = *d.Automated
	}
	indices := make(map[string]int, len(d.Steps))
	for i, step := range d.Steps {
		indices[step.ID] = i
	}
	inDegree := make([]int, len(d.Steps))
	children := make([][]int, len(d.Steps))
	descriptors := make([]RunStep, len(d.Steps))
	var ready []int
	for i, step := range d.Steps {
		run := RunStep{ID: step.ID, Command: append([]string(nil), step.Command...), WorkDir: baseDir, Automated: automated}
		if step.Run != nil {
			run.Command = []string{"sh", "-c", *step.Run}
		}
		if step.CWD != nil {
			run.WorkDir = *step.CWD
		}
		if workDirOverride != "" {
			run.WorkDir = workDirOverride
		}
		if step.Automated != nil {
			run.Automated = *step.Automated
		}
		for _, id := range step.After {
			run.Deps = append(run.Deps, model.Dep{Key: id, Kind: model.DepAfter})
		}
		for _, id := range step.AfterSuccess {
			run.Deps = append(run.Deps, model.Dep{Key: id, Kind: model.DepAfterSuccess})
		}
		for _, dep := range run.Deps {
			children[indices[dep.Key]] = append(children[indices[dep.Key]], i)
		}
		inDegree[i] = len(run.Deps)
		if inDegree[i] == 0 {
			ready = append(ready, i)
		}
		descriptors[i] = run
	}
	ordered := make([]RunStep, 0, len(d.Steps))
	for len(ready) > 0 {
		i := ready[0]
		ready = ready[1:]
		ordered = append(ordered, descriptors[i])
		for _, child := range children[i] {
			inDegree[child]--
			if inDegree[child] == 0 {
				ready = append(ready, child)
			}
		}
		sort.Ints(ready)
	}
	return ordered, nil
}
