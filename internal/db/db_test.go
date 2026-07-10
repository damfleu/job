package db

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

func openMemDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func makeJob(key string) *model.Job {
	return &model.Job{
		Key:       key,
		Command:   []string{"make", "-j8"},
		WorkDir:   "/home/user/project",
		LogFile:   "/tmp/job.log",
		Status:    model.StatusPending,
		Hostname:  "myhost",
		Username:  "damien",
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
}

func TestInsertGet(t *testing.T) {
	db := openMemDB(t)
	job := makeJob("key1")

	require.NoError(t, db.Insert(job))

	got, err := db.Get("key1")
	require.NoError(t, err)
	assert.Equal(t, job.Key, got.Key)
	assert.Equal(t, job.Command, got.Command)
	assert.Equal(t, job.WorkDir, got.WorkDir)
	assert.Equal(t, job.Status, got.Status)
	assert.Equal(t, job.Hostname, got.Hostname)
	assert.WithinDuration(t, job.CreatedAt, got.CreatedAt, time.Millisecond)
	assert.Nil(t, got.ExitCode)
	assert.Nil(t, got.StartedAt)
	assert.Nil(t, got.StoppedAt)
	assert.Nil(t, got.Deps)
}

func TestInsertGetOptionalFields(t *testing.T) {
	db := openMemDB(t)

	now := time.Now().UTC().Truncate(time.Millisecond)
	job := makeJob("key2")
	job.Alias = "myalias"
	job.Status = model.StatusCompleted
	job.Reason = model.ReasonExited
	job.ExitCode = new(42)
	job.PID = 1234
	job.PGID = 1234
	job.StartedAt = &now
	job.StoppedAt = &now
	job.Deps = []model.Dep{
		{Key: "dep1", Kind: model.DepAfter},
		{Key: "dep2", Kind: model.DepAfterSuccess},
	}

	require.NoError(t, db.Insert(job))

	got, err := db.Get("key2")
	require.NoError(t, err)
	assert.Equal(t, "myalias", got.Alias)
	assert.Equal(t, model.StatusCompleted, got.Status)
	assert.Equal(t, model.ReasonExited, got.Reason)
	require.NotNil(t, got.ExitCode)
	assert.Equal(t, 42, *got.ExitCode)
	assert.Equal(t, 1234, got.PID)
	assert.WithinDuration(t, now, *got.StartedAt, time.Millisecond)
	assert.WithinDuration(t, now, *got.StoppedAt, time.Millisecond)
	require.Len(t, got.Deps, 2)
	assert.Equal(t, "dep1", got.Deps[0].Key)
	assert.Equal(t, model.DepAfterSuccess, got.Deps[1].Kind)
}

func TestUpdate(t *testing.T) {
	db := openMemDB(t)
	job := makeJob("key3")
	require.NoError(t, db.Insert(job))

	now := time.Now().UTC().Truncate(time.Millisecond)
	job.Status = model.StatusCompleted
	job.Reason = model.ReasonExited
	job.ExitCode = new(0)
	job.StartedAt = &now
	job.StoppedAt = &now

	require.NoError(t, db.Update(job))

	got, err := db.Get("key3")
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, got.Status)
	assert.Equal(t, model.ReasonExited, got.Reason)
	require.NotNil(t, got.ExitCode)
	assert.Equal(t, 0, *got.ExitCode)
}

func TestUpdateNotFound(t *testing.T) {
	db := openMemDB(t)
	err := db.Update(makeJob("missing"))
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestDelete(t *testing.T) {
	db := openMemDB(t)
	job := makeJob("key4")
	require.NoError(t, db.Insert(job))

	require.NoError(t, db.Delete("key4"))
	_, err := db.Get("key4")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestDeleteNotFound(t *testing.T) {
	db := openMemDB(t)
	assert.ErrorIs(t, db.Delete("missing"), ErrNotFound)
}

func TestGetNotFound(t *testing.T) {
	db := openMemDB(t)
	_, err := db.Get("missing")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestListActive(t *testing.T) {
	db := openMemDB(t)

	active := makeJob("active1")
	blocked := makeJob("blocked1")
	blocked.Status = model.StatusBlocked
	completed := makeJob("done1")
	completed.Status = model.StatusCompleted

	require.NoError(t, db.Insert(active))
	require.NoError(t, db.Insert(blocked))
	require.NoError(t, db.Insert(completed))

	jobs, err := db.ListActive("", "")
	require.NoError(t, err)
	assert.Len(t, jobs, 2)
	keys := []string{jobs[0].Key, jobs[1].Key}
	assert.ElementsMatch(t, []string{"active1", "blocked1"}, keys)
}

func TestListCompleted(t *testing.T) {
	db := openMemDB(t)

	now := time.Now().UTC()
	for i, key := range []string{"c1", "c2", "c3"} {
		j := makeJob(key)
		j.Status = model.StatusCompleted
		offset := time.Duration(i) * time.Second
		j.StoppedAt = new(now.Add(offset).Truncate(time.Millisecond))
		require.NoError(t, db.Insert(j))
	}
	require.NoError(t, db.Insert(makeJob("active1"))) // not completed

	jobs, err := db.ListCompleted(10, "", "")
	require.NoError(t, err)
	require.Len(t, jobs, 3)
	// most recent first
	assert.Equal(t, "c3", jobs[0].Key)
	assert.Equal(t, "c2", jobs[1].Key)
	assert.Equal(t, "c1", jobs[2].Key)

	// limit
	limited, err := db.ListCompleted(2, "", "")
	require.NoError(t, err)
	assert.Len(t, limited, 2)
}

func TestListDepFailed(t *testing.T) {
	db := openMemDB(t)

	depFailed := makeJob("dep-failed-1")
	depFailed.Status = model.StatusCompleted
	depFailed.Reason = model.ReasonDepFailed
	require.NoError(t, db.Insert(depFailed))

	exited := makeJob("exited-1")
	exited.Status = model.StatusCompleted
	exited.Reason = model.ReasonExited
	require.NoError(t, db.Insert(exited))

	require.NoError(t, db.Insert(makeJob("active1"))) // not completed at all

	jobs, err := db.ListDepFailed()
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "dep-failed-1", jobs[0].Key)
}

func TestListActiveFilter(t *testing.T) {
	db := openMemDB(t)

	make1 := makeJob("k1")
	make1.Command = []string{"make", "-j8"}
	go1 := makeJob("k2")
	go1.Command = []string{"go", "test", "./..."}

	require.NoError(t, db.Insert(make1))
	require.NoError(t, db.Insert(go1))

	results, err := db.ListActive("make", "")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "k1", results[0].Key)

	all, err := db.ListActive("", "")
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestListCompletedFilter(t *testing.T) {
	db := openMemDB(t)

	now := time.Now().UTC()
	for i, cmd := range [][]string{{"make", "-j8"}, {"go", "test"}, {"make", "install"}} {
		j := makeJob(fmt.Sprintf("k%d", i+1))
		j.Status = model.StatusCompleted
		j.StoppedAt = new(now.Add(time.Duration(i) * time.Second).Truncate(time.Millisecond))
		j.Command = cmd
		require.NoError(t, db.Insert(j))
	}

	// regex matches "make" commands only
	results, err := db.ListCompleted(10, "^make", "")
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, j := range results {
		assert.Equal(t, "make", j.Command[0])
	}

	// limit applies after filter
	limited, err := db.ListCompleted(1, "^make", "")
	require.NoError(t, err)
	assert.Len(t, limited, 1)
}

func TestSearch(t *testing.T) {
	db := openMemDB(t)

	j1 := makeJob("k1")
	j1.Command = []string{"make", "-j8"}
	j2 := makeJob("k2")
	j2.Command = []string{"go", "test", "./..."}

	require.NoError(t, db.Insert(j1))
	require.NoError(t, db.Insert(j2))

	results, err := db.Search("make", "")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "k1", results[0].Key)

	all, err := db.Search("go", "")
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestSearchLimit(t *testing.T) {
	db := openMemDB(t)

	now := time.Now().UTC()
	for i := 0; i < searchLimit+5; i++ {
		j := makeJob(fmt.Sprintf("k%d", i))
		j.CreatedAt = now.Add(time.Duration(i) * time.Second).Truncate(time.Millisecond)
		require.NoError(t, db.Insert(j))
	}

	results, err := db.Search("make", "")
	require.NoError(t, err)
	require.Len(t, results, searchLimit)
	// most recently created first
	assert.Equal(t, fmt.Sprintf("k%d", searchLimit+4), results[0].Key)
}

func TestSearchContext(t *testing.T) {
	db := openMemDB(t)

	a := makeJob("a1")
	a.Command = []string{"make", "build"}
	a.Context = "projectA"
	b := makeJob("b1")
	b.Command = []string{"make", "build"}
	b.Context = "projectB"
	require.NoError(t, db.Insert(a))
	require.NoError(t, db.Insert(b))

	results, err := db.Search("make", "projectA")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "a1", results[0].Key)
}

func TestSearchEmptyQueryMatchesAll(t *testing.T) {
	db := openMemDB(t)

	now := time.Now().UTC()
	for i, key := range []string{"r1", "r2", "r3"} {
		j := makeJob(key)
		j.CreatedAt = now.Add(time.Duration(i) * time.Second).Truncate(time.Millisecond)
		require.NoError(t, db.Insert(j))
	}

	// an empty query is the picker's "no filter, just browse recent jobs" case
	jobs, err := db.Search("", "")
	require.NoError(t, err)
	require.Len(t, jobs, 3)
	assert.Equal(t, "r3", jobs[0].Key)
	assert.Equal(t, "r2", jobs[1].Key)
	assert.Equal(t, "r1", jobs[2].Key)
}

func TestFindByAliasMostRecent(t *testing.T) {
	db := openMemDB(t)

	earlier := makeJob("old_key")
	earlier.Alias = "foo"
	earlier.Status = model.StatusCompleted
	earlier.CreatedAt = time.Now().UTC().Add(-time.Hour)
	require.NoError(t, db.Insert(earlier))

	later := makeJob("new_key")
	later.Alias = "foo"
	later.Status = model.StatusRunning
	later.CreatedAt = time.Now().UTC()
	require.NoError(t, db.Insert(later))

	got, err := db.FindByAlias("foo", "")
	require.NoError(t, err)
	assert.Equal(t, "new_key", got.Key, "should return most recently created job")
}

func TestFindByAliasScopedToContext(t *testing.T) {
	db := openMemDB(t)

	a := makeJob("a1")
	a.Alias = "build"
	a.Context = "projectA"
	b := makeJob("b1")
	b.Alias = "build"
	b.Context = "projectB"
	require.NoError(t, db.Insert(a))
	require.NoError(t, db.Insert(b))

	got, err := db.FindByAlias("build", "projectA")
	require.NoError(t, err)
	assert.Equal(t, "a1", got.Key)

	got, err = db.FindByAlias("build", "projectB")
	require.NoError(t, err)
	assert.Equal(t, "b1", got.Key)

	_, err = db.FindByAlias("build", "")
	require.NoError(t, err)
}

func TestFindByKeyPrefix(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, db.Insert(makeJob("1712912345_abcd_make")))

	results, err := db.FindByKeyPrefix("1712912345")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "1712912345_abcd_make", results[0].Key)
}

func TestFindByKeyPrefixMultipleOrderedByNewest(t *testing.T) {
	db := openMemDB(t)

	now := time.Now().UTC()
	older := makeJob("1712912345_aaaa_make")
	older.CreatedAt = now.Add(-time.Minute).Truncate(time.Millisecond)
	newer := makeJob("1712912345_bbbb_go")
	newer.CreatedAt = now.Truncate(time.Millisecond)

	require.NoError(t, db.Insert(older))
	require.NoError(t, db.Insert(newer))

	results, err := db.FindByKeyPrefix("1712912345")
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "1712912345_bbbb_go", results[0].Key, "most recently created should be first")
}

func TestFindByKeyPrefixNoMatch(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, db.Insert(makeJob("1712912345_abcd_make")))

	results, err := db.FindByKeyPrefix("9999999999")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestListCompletedBefore(t *testing.T) {
	db := openMemDB(t)

	now := time.Now().UTC()
	for i, key := range []string{"old1", "old2", "new1"} {
		j := makeJob(key)
		j.Status = model.StatusCompleted
		j.StoppedAt = new(now.Add(time.Duration(i) * time.Hour).Truncate(time.Millisecond))
		require.NoError(t, db.Insert(j))
	}
	require.NoError(t, db.Insert(makeJob("active1"))) // not completed, must be excluded

	cutoff := now.Add(2 * time.Hour) // old1 and old2 are before this, new1 is not
	jobs, err := db.ListCompletedBefore(cutoff, "")
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	keys := []string{jobs[0].Key, jobs[1].Key}
	assert.ElementsMatch(t, []string{"old1", "old2"}, keys)
}

func TestListActiveContext(t *testing.T) {
	db := openMemDB(t)

	a := makeJob("a1")
	a.Context = "projectA"
	b := makeJob("b1")
	b.Context = "projectB"
	require.NoError(t, db.Insert(a))
	require.NoError(t, db.Insert(b))

	results, err := db.ListActive("", "projectA")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "a1", results[0].Key)
}

func TestListCompletedContext(t *testing.T) {
	db := openMemDB(t)

	now := time.Now().UTC()
	a := makeJob("a1")
	a.Status = model.StatusCompleted
	a.Context = "projectA"
	a.StoppedAt = new(now.Truncate(time.Millisecond))
	b := makeJob("b1")
	b.Status = model.StatusCompleted
	b.Context = "projectB"
	b.StoppedAt = new(now.Add(time.Second).Truncate(time.Millisecond))
	require.NoError(t, db.Insert(a))
	require.NoError(t, db.Insert(b))

	results, err := db.ListCompleted(10, "", "projectA")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "a1", results[0].Key)
}

func TestGetLastKeyForContext(t *testing.T) {
	db := openMemDB(t)

	now := time.Now().UTC()
	older := makeJob("older")
	older.Context = "projectA"
	older.CreatedAt = now.Add(-time.Minute).Truncate(time.Millisecond)
	newer := makeJob("newer")
	newer.Context = "projectA"
	newer.CreatedAt = now.Truncate(time.Millisecond)
	other := makeJob("other")
	other.Context = "projectB"
	other.CreatedAt = now.Add(time.Minute).Truncate(time.Millisecond)
	require.NoError(t, db.Insert(older))
	require.NoError(t, db.Insert(newer))
	require.NoError(t, db.Insert(other))

	// returns most recent job in the given context
	key, err := db.GetLastKeyForContext("projectA")
	require.NoError(t, err)
	assert.Equal(t, "newer", key)

	// different context is unaffected
	key, err = db.GetLastKeyForContext("projectB")
	require.NoError(t, err)
	assert.Equal(t, "other", key)

	// unknown context returns empty string
	key, err = db.GetLastKeyForContext("unknown")
	require.NoError(t, err)
	assert.Empty(t, key)
}

func TestGetLastKeyForContextExcludesAutomated(t *testing.T) {
	db := openMemDB(t)

	automated := makeJob("automated")
	automated.Context = "projectA"
	automated.Automated = true
	human := makeJob("human")
	human.Context = "projectA"
	human.CreatedAt = time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	require.NoError(t, db.Insert(automated))
	require.NoError(t, db.Insert(human))

	// automated job should not be returned
	key, err := db.GetLastKeyForContext("projectA")
	require.NoError(t, err)
	assert.Equal(t, "human", key)
}

func TestGetLastKeyForContextEmptyIsGlobal(t *testing.T) {
	db := openMemDB(t)

	now := time.Now().UTC()
	a := makeJob("a1")
	a.Context = "projectA"
	a.CreatedAt = now.Add(-time.Minute).Truncate(time.Millisecond)
	b := makeJob("b1")
	b.Context = "projectB"
	b.CreatedAt = now.Truncate(time.Millisecond)
	require.NoError(t, db.Insert(a))
	require.NoError(t, db.Insert(b))

	key, err := db.GetLastKeyForContext("")
	require.NoError(t, err)
	assert.Equal(t, "b1", key, "empty context should return the most recent job across all contexts")
}

func TestGetLastKeyByStatus(t *testing.T) {
	db := openMemDB(t)

	now := time.Now().UTC()
	older := makeJob("older")
	older.Status = model.StatusRunning
	older.CreatedAt = now.Add(-time.Minute).Truncate(time.Millisecond)
	newer := makeJob("newer")
	newer.Status = model.StatusRunning
	newer.CreatedAt = now.Truncate(time.Millisecond)
	completed := makeJob("completed")
	completed.Status = model.StatusCompleted
	completed.CreatedAt = now.Add(time.Minute).Truncate(time.Millisecond)
	require.NoError(t, db.Insert(older))
	require.NoError(t, db.Insert(newer))
	require.NoError(t, db.Insert(completed))

	key, err := db.GetLastKeyByStatus(model.StatusRunning, "")
	require.NoError(t, err)
	assert.Equal(t, "newer", key)
}

func TestGetLastKeyByStatusContext(t *testing.T) {
	db := openMemDB(t)

	a := makeJob("a1")
	a.Status = model.StatusRunning
	a.Context = "projectA"
	b := makeJob("b1")
	b.Status = model.StatusRunning
	b.Context = "projectB"
	require.NoError(t, db.Insert(a))
	require.NoError(t, db.Insert(b))

	key, err := db.GetLastKeyByStatus(model.StatusRunning, "projectA")
	require.NoError(t, err)
	assert.Equal(t, "a1", key)
}

func TestGetLastKeyByStatusNoMatch(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, db.Insert(makeJob("k1")))

	key, err := db.GetLastKeyByStatus(model.StatusRunning, "")
	require.NoError(t, err)
	assert.Empty(t, key)
}

func TestGetLastKeyByStatusExcludesAutomated(t *testing.T) {
	db := openMemDB(t)

	automated := makeJob("automated")
	automated.Status = model.StatusRunning
	automated.Automated = true
	human := makeJob("human")
	human.Status = model.StatusRunning
	human.CreatedAt = time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	require.NoError(t, db.Insert(automated))
	require.NoError(t, db.Insert(human))

	key, err := db.GetLastKeyByStatus(model.StatusRunning, "")
	require.NoError(t, err)
	assert.Equal(t, "human", key)
}

func TestGetByKeys(t *testing.T) {
	db := openMemDB(t)

	j1 := makeJob("k1")
	j2 := makeJob("k2")
	j3 := makeJob("k3")
	require.NoError(t, db.Insert(j1))
	require.NoError(t, db.Insert(j2))
	require.NoError(t, db.Insert(j3))

	got, err := db.GetByKeys([]string{"k1", "k3"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	keys := []string{got[0].Key, got[1].Key}
	assert.ElementsMatch(t, []string{"k1", "k3"}, keys)
}

func TestGetByKeysEmpty(t *testing.T) {
	db := openMemDB(t)
	got, err := db.GetByKeys(nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestGetByKeysUnknown(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, db.Insert(makeJob("k1")))

	got, err := db.GetByKeys([]string{"k1", "missing"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "k1", got[0].Key)
}
