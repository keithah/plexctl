// Package maintenancestatus provides pure, deterministic maintenance-status analysis.
package maintenancestatus

import (
	"fmt"
	"sort"
	"strings"
)

// Activity is a normalized, safe maintenance activity record.
type Activity struct {
	ID, Type, Title string
	Progress        *float64
	Cancellable     *bool
}

// Task is a normalized, safe scheduled-maintenance task record.
type Task struct {
	ID, Title, Schedule string
	Enabled             *bool
	Interval            *int64
}

// Updater is normalized updater state. Download locations are deliberately excluded.
type Updater struct {
	CanInstall  *bool
	Version     string
	ReleaseDate string
}

// Snapshot groups caller-supplied maintenance status blocks.
type Snapshot struct {
	Activities []Activity
	Tasks      []Task
	Updater    Updater
}

// Report preserves separate, stable maintenance status blocks.
type Report struct {
	Activities []Activity
	Tasks      []Task
	Updater    Updater
}

// Analyze validates and stably sorts activities and tasks without merging their blocks.
func Analyze(snapshot Snapshot) (Report, error) {
	activities := append([]Activity(nil), snapshot.Activities...)
	for index, activity := range activities {
		if strings.TrimSpace(activity.ID) == "" {
			return Report{}, fmt.Errorf("activity %d has blank identifier", index)
		}
	}
	tasks := append([]Task(nil), snapshot.Tasks...)
	for index, task := range tasks {
		if strings.TrimSpace(task.ID) == "" {
			return Report{}, fmt.Errorf("task %d has blank identifier", index)
		}
	}
	sort.Slice(activities, func(i, j int) bool { return activities[i].ID < activities[j].ID })
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return Report{Activities: activities, Tasks: tasks, Updater: snapshot.Updater}, nil
}
