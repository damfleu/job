package db

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"job/internal/model"
)

func TestSaveSequenceRoundTrip(t *testing.T) {
	d := openMemDB(t)
	want := &model.Sequence{
		Name: "workflow",
		Steps: []model.SequenceStep{
			{ID: 1, Command: []string{"echo", "one"}, WorkDir: "/tmp"},
			{
				ID:      2,
				Command: []string{"echo", "two"},
				WorkDir: "/tmp",
				Deps: []model.SequenceDep{
					{StepID: 1, Kind: model.DepAfterSuccess},
				},
			},
		},
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, d.SaveSequence(want))

	got, err := d.GetSequence("workflow")
	require.NoError(t, err)
	assert.Equal(t, want, got)

	summaries, err := d.ListSequences()
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, "workflow", summaries[0].Name)
	assert.Len(t, summaries[0].Steps, 2)
}

func TestSaveSequenceRejectsInvalidDefinition(t *testing.T) {
	d := openMemDB(t)
	err := d.SaveSequence(&model.Sequence{
		Name: "invalid",
		Steps: []model.SequenceStep{
			{
				ID:      1,
				Command: []string{"echo", "one"},
				Deps: []model.SequenceDep{
					{StepID: 2, Kind: model.DepAfterSuccess},
				},
			},
			{ID: 2, Command: []string{"echo", "two"}},
		},
		CreatedAt: time.Now().UTC(),
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "depends on missing or later step 2")
}
