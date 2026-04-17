package core

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/db"
	"job/internal/model"
)

// fakeStore implements db.JobStore for testing resolve logic.
type fakeStore struct {
	jobs    []*model.Job
	lastKey string
}

func (f *fakeStore) Get(key string) (*model.Job, error) {
	for _, j := range f.jobs {
		if j.Key == key {
			return j, nil
		}
	}
	return nil, db.ErrNotFound
}

func (f *fakeStore) FindByAlias(alias string) (*model.Job, error) {
	for _, j := range f.jobs {
		if j.Alias == alias {
			return j, nil
		}
	}
	return nil, db.ErrNotFound
}

func (f *fakeStore) Search(query string) ([]*model.Job, error) {
	var out []*model.Job
	for _, j := range f.jobs {
		if strings.Contains(strings.Join(j.Command, " "), query) {
			out = append(out, j)
		}
	}
	return out, nil
}

func (f *fakeStore) FindByKeyPrefix(prefix string) ([]*model.Job, error) {
	var out []*model.Job
	for _, j := range f.jobs {
		if strings.HasPrefix(j.Key, prefix) {
			out = append(out, j)
		}
	}
	return out, nil
}

func (f *fakeStore) GetLastKey() (string, error) { return f.lastKey, nil }
func (f *fakeStore) GetLastKeyForContext(context string) (string, error) {
	for _, j := range f.jobs {
		if j.Context == context {
			return j.Key, nil
		}
	}
	return "", nil
}
func (f *fakeStore) SetLastKey(key string) error { f.lastKey = key; return nil }
func (f *fakeStore) Insert(job *model.Job) error         { return nil }
func (f *fakeStore) Update(job *model.Job) error         { return nil }
func (f *fakeStore) Delete(key string) error             { return nil }
func (f *fakeStore) ListActive(filter, context string) ([]*model.Job, error)               { return nil, nil }
func (f *fakeStore) ListCompleted(limit int, filter, context string) ([]*model.Job, error) { return nil, nil }
func (f *fakeStore) ListCompletedBefore(t time.Time, context string) ([]*model.Job, error) { return nil, nil }
func (f *fakeStore) SaveSequence(seq *model.Sequence) error                                { return nil }
func (f *fakeStore) GetSequence(name string) (*model.Sequence, error)                      { return nil, nil }
func (f *fakeStore) ListSequences() ([]*model.Sequence, error)                             { return nil, nil }
func (f *fakeStore) DeleteSequence(name string) error                                      { return nil }
func (f *fakeStore) SequencesForJob(jobKey string) ([]string, error)                       { return nil, nil }

func job(key, alias string, cmd []string, status model.Status) *model.Job {
	return &model.Job{Key: key, Alias: alias, Command: cmd, Status: status}
}

func TestResolveExactKey(t *testing.T) {
	store := &fakeStore{jobs: []*model.Job{
		job("1234_abcd_make", "", []string{"make"}, model.StatusRunning),
	}}
	j, err := ResolveKey(store, "1234_abcd_make", "")
	require.NoError(t, err)
	assert.Equal(t, "1234_abcd_make", j.Key)
}

func TestResolveAlias(t *testing.T) {
	store := &fakeStore{jobs: []*model.Job{
		job("1234_abcd_make", "mybuild", []string{"make"}, model.StatusRunning),
	}}
	j, err := ResolveKey(store, "mybuild", "")
	require.NoError(t, err)
	assert.Equal(t, "1234_abcd_make", j.Key)
}

func TestResolveCommandSubstring(t *testing.T) {
	store := &fakeStore{jobs: []*model.Job{
		job("key1", "", []string{"make", "-j8"}, model.StatusCompleted),
		job("key2", "", []string{"go", "test", "./..."}, model.StatusRunning),
	}}
	j, err := ResolveKey(store, "go test", "")
	require.NoError(t, err)
	assert.Equal(t, "key2", j.Key)
}

func TestResolveCommandSubstringPrefersActive(t *testing.T) {
	store := &fakeStore{jobs: []*model.Job{
		job("key1", "", []string{"make"}, model.StatusCompleted),
		job("key2", "", []string{"make"}, model.StatusRunning),
	}}
	j, err := ResolveKey(store, "make", "")
	require.NoError(t, err)
	assert.Equal(t, "key2", j.Key)
}

func TestResolveCommandSubstringFallsBackToCompleted(t *testing.T) {
	store := &fakeStore{jobs: []*model.Job{
		job("key1", "", []string{"make"}, model.StatusCompleted),
	}}
	j, err := ResolveKey(store, "make", "")
	require.NoError(t, err)
	assert.Equal(t, "key1", j.Key)
}

func TestResolveKeyPrefix(t *testing.T) {
	store := &fakeStore{jobs: []*model.Job{
		job("1712912345_abcd_make", "", []string{"make"}, model.StatusCompleted),
	}}
	j, err := ResolveKey(store, "1712912345", "")
	require.NoError(t, err)
	assert.Equal(t, "1712912345_abcd_make", j.Key)
}

func TestResolveDot(t *testing.T) {
	store := &fakeStore{
		jobs:    []*model.Job{job("thekey", "", []string{"make"}, model.StatusCompleted)},
		lastKey: "thekey",
	}
	j, err := ResolveKey(store, ".", "")
	require.NoError(t, err)
	assert.Equal(t, "thekey", j.Key)
}

func TestResolveDotNoJobs(t *testing.T) {
	store := &fakeStore{}
	_, err := ResolveKey(store, ".", "")
	assert.Error(t, err)
}

func TestResolveCommandSubstringMultipleActive(t *testing.T) {
	// two active jobs match — first non-completed result from the store is returned
	store := &fakeStore{jobs: []*model.Job{
		job("key1", "", []string{"make", "build"}, model.StatusRunning),
		job("key2", "", []string{"make", "test"}, model.StatusRunning),
	}}
	j, err := ResolveKey(store, "make", "")
	require.NoError(t, err)
	assert.Equal(t, "key1", j.Key)
}

func TestResolveKeyPrefixMultipleMatches(t *testing.T) {
	// multiple keys share the same prefix — first result from the store is returned
	store := &fakeStore{jobs: []*model.Job{
		job("1712912345_aaaa_make", "", []string{"make"}, model.StatusCompleted),
		job("1712912345_bbbb_go", "", []string{"go"}, model.StatusRunning),
	}}
	j, err := ResolveKey(store, "1712912345", "")
	require.NoError(t, err)
	assert.Equal(t, "1712912345_aaaa_make", j.Key)
}

func TestResolveNoMatch(t *testing.T) {
	store := &fakeStore{jobs: []*model.Job{
		job("key1", "", []string{"make"}, model.StatusRunning),
	}}
	_, err := ResolveKey(store, "nonexistent", "")
	assert.Error(t, err)
}

func TestResolveDotInContext(t *testing.T) {
	projectA := job("key_a", "", []string{"make"}, model.StatusCompleted)
	projectA.Context = "projectA"
	projectB := job("key_b", "", []string{"go test"}, model.StatusCompleted)
	projectB.Context = "projectB"
	store := &fakeStore{jobs: []*model.Job{projectA, projectB}, lastKey: "key_b"}

	// --here with context "projectA" should return key_a, not the global last key (key_b)
	j, err := ResolveKey(store, ".", "projectA")
	require.NoError(t, err)
	assert.Equal(t, "key_a", j.Key)
}

func TestResolveDotInContextNoMatch(t *testing.T) {
	j := job("key_a", "", []string{"make"}, model.StatusCompleted)
	j.Context = "projectA"
	store := &fakeStore{jobs: []*model.Job{j}}

	_, err := ResolveKey(store, ".", "projectB")
	assert.Error(t, err)
}
