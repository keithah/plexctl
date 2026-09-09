package maintenancestatus_test

import (
	"reflect"
	"testing"

	"github.com/keithah/plexctl/internal/maintenancestatus"
)

func TestAnalyzeKeepsSortedBlocksSeparate(t *testing.T) {
	complete := true
	progress := 42.5
	report, err := maintenancestatus.Analyze(maintenancestatus.Snapshot{
		Activities: []maintenancestatus.Activity{
			{ID: "a-2", Type: "refresh", Title: "Refresh", Progress: &progress},
			{ID: "a-1", Type: "analyze", Title: "Analyze"},
		},
		Tasks: []maintenancestatus.Task{
			{ID: "task-b", Title: "B", Enabled: &complete},
			{ID: "task-a", Title: "A"},
		},
		Updater: maintenancestatus.Updater{Version: "1.2.3", CanInstall: &complete},
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if got, want := report.Activities, []maintenancestatus.Activity{{ID: "a-1", Type: "analyze", Title: "Analyze"}, {ID: "a-2", Type: "refresh", Title: "Refresh", Progress: &progress}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Activities = %#v, want %#v", got, want)
	}
	if got, want := report.Tasks, []maintenancestatus.Task{{ID: "task-a", Title: "A"}, {ID: "task-b", Title: "B", Enabled: &complete}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Tasks = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(report.Updater, maintenancestatus.Updater{Version: "1.2.3", CanInstall: &complete}) {
		t.Fatalf("Updater = %#v", report.Updater)
	}
}

func TestAnalyzeRejectsBlankActivityOrTaskID(t *testing.T) {
	for _, snapshot := range []maintenancestatus.Snapshot{
		{Activities: []maintenancestatus.Activity{{ID: " "}}},
		{Tasks: []maintenancestatus.Task{{ID: ""}}},
	} {
		if _, err := maintenancestatus.Analyze(snapshot); err == nil {
			t.Fatal("Analyze() succeeded for malformed necessary identifier")
		}
	}
}

func TestAnalyzeRejectsDuplicateActivityOrTaskID(t *testing.T) {
	for name, snapshot := range map[string]maintenancestatus.Snapshot{
		"activity": {Activities: []maintenancestatus.Activity{{ID: "activity-1", Title: "First"}, {ID: "activity-1", Title: "Second"}}},
		"task":     {Tasks: []maintenancestatus.Task{{ID: "task-1", Title: "First"}, {ID: "task-1", Title: "Second"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := maintenancestatus.Analyze(snapshot); err == nil {
				t.Fatal("Analyze() succeeded for duplicate opaque identity")
			}
		})
	}
}
