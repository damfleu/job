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
	code := 42
	job := makeJob("key2")
	job.Alias = "myalias"
	job.Status = model.StatusCompleted
	job.Reason = model.ReasonExited
	job.ExitCode = &code
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
	code := 0
	job.Status = model.StatusCompleted
	job.Reason = model.ReasonExited
	job.ExitCode = &code
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

	jobs, err := db.ListActive("")
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
		t2 := now.Add(offset).Truncate(time.Millisecond)
		j.StoppedAt = &t2
		require.NoError(t, db.Insert(j))
	}
	require.NoError(t, db.Insert(makeJob("active1"))) // not completed

	jobs, err := db.ListCompleted(10, "")
	require.NoError(t, err)
	require.Len(t, jobs, 3)
	// most recent first
	assert.Equal(t, "c3", jobs[0].Key)
	assert.Equal(t, "c2", jobs[1].Key)
	assert.Equal(t, "c1", jobs[2].Key)

	// limit
	limited, err := db.ListCompleted(2, "")
	require.NoError(t, err)
	assert.Len(t, limited, 2)
}

func TestListActiveFilter(t *testing.T) {
	db := openMemDB(t)

	make1 := makeJob("k1")
	make1.Command = []string{"make", "-j8"}
	go1 := makeJob("k2")
	go1.Command = []string{"go", "test", "./..."}

	require.NoError(t, db.Insert(make1))
	require.NoError(t, db.Insert(go1))

	results, err := db.ListActive("make")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "k1", results[0].Key)

	all, err := db.ListActive("")
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestListCompletedFilter(t *testing.T) {
	db := openMemDB(t)

	now := time.Now().UTC()
	for i, cmd := range [][]string{{"make", "-j8"}, {"go", "test"}, {"make", "install"}} {
		j := makeJob(fmt.Sprintf("k%d", i+1))
		j.Status = model.StatusCompleted
		t2 := now.Add(time.Duration(i) * time.Second).Truncate(time.Millisecond)
		j.StoppedAt = &t2
		j.Command = cmd
		require.NoError(t, db.Insert(j))
	}

	// regex matches "make" commands only
	results, err := db.ListCompleted(10, "^make")
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, j := range results {
		assert.Equal(t, "make", j.Command[0])
	}

	// limit applies after filter
	limited, err := db.ListCompleted(1, "^make")
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

	results, err := db.Search("make")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "k1", results[0].Key)

	all, err := db.Search("go")
	require.NoError(t, err)
	assert.Len(t, all, 1)
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

	got, err := db.FindByAlias("foo")
	require.NoError(t, err)
	assert.Equal(t, "new_key", got.Key, "should return most recently created job")
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
		t2 := now.Add(time.Duration(i) * time.Hour).Truncate(time.Millisecond)
		j.StoppedAt = &t2
		require.NoError(t, db.Insert(j))
	}
	require.NoError(t, db.Insert(makeJob("active1"))) // not completed, must be excluded

	cutoff := now.Add(2 * time.Hour) // old1 and old2 are before this, new1 is not
	jobs, err := db.ListCompletedBefore(cutoff)
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	keys := []string{jobs[0].Key, jobs[1].Key}
	assert.ElementsMatch(t, []string{"old1", "old2"}, keys)
}

func TestLastKey(t *testing.T) {
	db := openMemDB(t)

	key, err := db.GetLastKey()
	require.NoError(t, err)
	assert.Empty(t, key)

	require.NoError(t, db.SetLastKey("abc123"))
	key, err = db.GetLastKey()
	require.NoError(t, err)
	assert.Equal(t, "abc123", key)

	// overwrite
	require.NoError(t, db.SetLastKey("xyz999"))
	key, err = db.GetLastKey()
	require.NoError(t, err)
	assert.Equal(t, "xyz999", key)
}
