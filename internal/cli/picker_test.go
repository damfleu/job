package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

func TestCandidatesForQuery(t *testing.T) {
	d := openTestDB(t)
	require.NoError(t, d.Insert(makeTestJob("make1", model.StatusRunning)))
	require.NoError(t, d.Insert(makeTestJob("go1", model.StatusRunning)))

	got, err := candidatesFor(d, "make1", "")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "make1", got[0].Key)
}

func TestCandidatesForDot(t *testing.T) {
	d := openTestDB(t)
	require.NoError(t, d.Insert(makeTestJob("a", model.StatusRunning)))
	require.NoError(t, d.Insert(makeTestJob("b", model.StatusCompleted)))

	got, err := candidatesFor(d, ".", "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, jobKeys(got))
}

func TestCandidatesForContext(t *testing.T) {
	d := openTestDB(t)
	a := makeTestJob("a", model.StatusRunning)
	a.Context = "projectA"
	b := makeTestJob("b", model.StatusRunning)
	b.Context = "projectB"
	require.NoError(t, d.Insert(a))
	require.NoError(t, d.Insert(b))

	got, err := candidatesFor(d, ".", "projectA")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].Key)
}

func TestCandidateHeader(t *testing.T) {
	header := candidateHeader()
	assert.Contains(t, header, "KEY")
	assert.Contains(t, header, "STATUS")
	assert.Contains(t, header, "TIME")
	assert.Contains(t, header, "COMMAND")
}

func TestCandidateLabel(t *testing.T) {
	j := makeTestJob("mykey", model.StatusRunning)
	label := candidateLabel(j)
	assert.Contains(t, label, "mykey")
	// jobStatusStyle embeds ANSI codes, so assert on substring rather than exact match
	assert.Contains(t, label, "running")
	assert.Contains(t, label, strings.Join(j.Command, " "))
}
