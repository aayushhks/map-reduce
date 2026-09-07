package mr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The status snapshot must follow the job through its phases, and must count
// the attempts actually in flight.
func TestStatusFollowsThePhases(t *testing.T) {
	c := newTestCoordinator(t, 2, 1, Config{})

	s := c.Status()
	if s.Phase != "map" || s.Done {
		t.Fatalf("fresh job reported phase=%q done=%v", s.Phase, s.Done)
	}
	if s.Map.Total != 2 || s.Map.Idle != 2 {
		t.Fatalf("map phase reported %+v", s.Map)
	}

	first := request(t, c, "worker-a")
	s = c.Status()
	if s.Map.InProgress != 1 || s.Map.Idle != 1 {
		t.Fatalf("after one assignment map is %+v", s.Map)
	}
	if len(s.Running) != 1 || s.Running[0].WorkerID != "worker-a" {
		t.Fatalf("running tasks: %+v", s.Running)
	}

	finish(t, c, first)
	finish(t, c, request(t, c, "worker-b"))

	s = c.Status()
	if s.Phase != "reduce" {
		t.Fatalf("phase after the map tasks finished: %q", s.Phase)
	}
	if s.Map.Completed != 2 {
		t.Fatalf("map completed %d, want 2", s.Map.Completed)
	}

	finish(t, c, request(t, c, "worker-a"))

	s = c.Status()
	if s.Phase != "done" || !s.Done {
		t.Fatalf("finished job reported phase=%q done=%v", s.Phase, s.Done)
	}
	if len(s.Running) != 0 {
		t.Fatalf("finished job still lists running tasks: %+v", s.Running)
	}

	byID := map[string]WorkerStatus{}
	for _, w := range s.Workers {
		byID[w.WorkerID] = w
	}
	if byID["worker-a"].Committed != 2 || byID["worker-b"].Committed != 1 {
		t.Fatalf("worker totals: %+v", s.Workers)
	}
}

// The endpoint must serve valid JSON that round trips into the same shape.
func TestStatusEndpointServesJSON(t *testing.T) {
	c := newTestCoordinator(t, 1, 1, Config{})
	request(t, c, "worker-a")

	recorder := httptest.NewRecorder()
	c.handleStatus(recorder, httptest.NewRequest(http.MethodGet, "/status", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type %q", got)
	}

	var decoded Status
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode status: %v\n%s", err, recorder.Body.String())
	}
	if decoded.Phase != "map" || decoded.Map.InProgress != 1 {
		t.Fatalf("decoded status: %+v", decoded)
	}
}
