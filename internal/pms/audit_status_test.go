package pms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keithah/plexctl/internal/api"
)

func TestAuditStatusClientsUseOnlyGET(t *testing.T) {
	paths := map[string]bool{
		"/activities":     false,
		"/butler":         false,
		"/updater/status": false,
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("%s method = %s, want GET", r.URL.Path, r.Method)
		}
		if _, ok := paths[r.URL.Path]; !ok {
			http.NotFound(w, r)
			return
		}
		paths[r.URL.Path] = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"MediaContainer":{"size":0}}`))
	}))
	defer s.Close()

	a, err := api.New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := New(a)
	if _, err := c.Activities(context.Background()); err != nil {
		t.Fatalf("Activities: %v", err)
	}
	if _, err := c.ButlerTasks(context.Background()); err != nil {
		t.Fatalf("ButlerTasks: %v", err)
	}
	if _, err := c.UpdaterStatus(context.Background()); err != nil {
		t.Fatalf("UpdaterStatus: %v", err)
	}
	for path, seen := range paths {
		if !seen {
			t.Errorf("no request to %s", path)
		}
	}
}

func TestAuditModelsDecodeReportFieldsAndPreserveOptionality(t *testing.T) {
	var metadata Metadata
	if err := json.Unmarshal([]byte(`{"Media":[{"Part":[{"key":"/library/parts/1","size":1234,"indexes":"sd","audioProfile":"lc","container":"mp4","videoProfile":"high","optimizedForStreaming":true,"hasThumbnail":true,"exists":true,"file":"/secret/movie.mp4","decision":"transcode"}]}]}`), &metadata); err != nil {
		t.Fatalf("decode media metadata: %v", err)
	}
	part := metadata.Media[0].Part[0]
	if part.Key != "/library/parts/1" || part.Size == nil || *part.Size != 1234 || part.ChangedAt != nil {
		t.Fatalf("part = %+v", part)
	}

	var sessions SessionContainer
	if err := json.Unmarshal([]byte(`{"MediaContainer":{"size":1,"Metadata":[{"ratingKey":"1","User":{"id":"7","title":"Ada"},"Player":{"machineIdentifier":"client-1","title":"Living Room","platform":"Roku","address":"192.0.2.1"},"Session":{"id":"s1","location":"lan","bandwidth":8000},"TranscodeSession":{"key":"t1","throttled":false,"speed":1.5,"progress":50.0}}]}}`), &sessions); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	session := sessions.MediaContainer.Metadata[0]
	if session.User.ID != "7" || session.Player.MachineIdentifier != "client-1" || session.Session.ID != "s1" || session.TranscodeSession.Key != "t1" || session.TranscodeSession.Speed == nil || *session.TranscodeSession.Speed != 1.5 {
		t.Fatalf("session = %+v", session)
	}
	if session.TranscodeSession.Complete != nil {
		t.Fatalf("omitted optional complete = %v, want nil", session.TranscodeSession.Complete)
	}

	var activities ActivitiesContainer
	if err := json.Unmarshal([]byte(`{"MediaContainer":{"size":1,"Activity":[{"uuid":"a1","type":"library.update","title":"Scanning","progress":25.5,"cancellable":true}]}}`), &activities); err != nil {
		t.Fatalf("decode activities: %v", err)
	}
	if len(activities.MediaContainer.Activity) != 1 || activities.MediaContainer.Activity[0].UUID != "a1" || activities.MediaContainer.Activity[0].Progress == nil {
		t.Fatalf("activities = %+v", activities)
	}

	var butler ButlerContainer
	if err := json.Unmarshal([]byte(`{"MediaContainer":{"size":1,"ButlerTask":[{"name":"BackupDatabase","title":"Backup","enabled":true,"interval":3600,"schedule":"daily"}]}}`), &butler); err != nil {
		t.Fatalf("decode Butler tasks: %v", err)
	}
	if len(butler.MediaContainer.ButlerTask) != 1 || butler.MediaContainer.ButlerTask[0].Name != "BackupDatabase" || butler.MediaContainer.ButlerTask[0].Enabled == nil || !*butler.MediaContainer.ButlerTask[0].Enabled {
		t.Fatalf("butler = %+v", butler)
	}

	var updater UpdaterStatusContainer
	if err := json.Unmarshal([]byte(`{"MediaContainer":{"canInstall":true,"version":"1.2.3","releaseDate":"2026-09-08","downloadURL":"https://example.invalid/update"}}`), &updater); err != nil {
		t.Fatalf("decode updater status: %v", err)
	}
	if updater.MediaContainer.CanInstall == nil || !*updater.MediaContainer.CanInstall || updater.MediaContainer.Version != "1.2.3" {
		t.Fatalf("updater = %+v", updater)
	}
}
