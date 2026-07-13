package core

import (
	"fmt"
	"os"

	"job/internal/db"
	"job/internal/model"
)

// DeleteJob removes a job from the database and deletes its log file.
func DeleteJob(store db.JobStore, j *model.Job) error {
	if j.Status != model.StatusCompleted {
		return fmt.Errorf("job %s is not completed (status: %s)", j.Key, j.Status)
	}
	if err := store.Delete(j.Key); err != nil {
		return err
	}
	if j.LogFile != "" {
		if err := os.Remove(j.LogFile); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
