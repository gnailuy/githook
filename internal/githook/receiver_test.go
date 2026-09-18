package githook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeQueue struct {
	added bool
	calls int
	err   error
}

func (f *fakeQueue) Enqueue(_ context.Context, _ string, _ int64, _ string) (bool, error) {
	f.calls++
	return f.added, f.err
}
func sign(body, secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}
func request(t *testing.T, r Receiver, method, event, body, sig string) *httptest.ResponseRecorder {
	t.Helper()
	q := httptest.NewRequest(method, "/", strings.NewReader(body))
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("X-GitHub-Event", event)
	q.Header.Set("X-GitHub-Delivery", "12345678-1234-1234-1234-123456789abc")
	q.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, q)
	return w
}
func TestReceiverWorkflowRunAndDedup(t *testing.T) {
	body := `{"action":"completed","repository":{"full_name":"example/project"},"workflow_run":{"id":42,"head_sha":"0123456789012345678901234567890123456789"}}`
	f := &fakeQueue{added: true}
	r := Receiver{Secret: []byte("test-secret"), Repository: "example/project", Queue: f}
	if got := request(t, r, http.MethodPost, "workflow_run", body, sign(body, "test-secret")).Code; got != http.StatusAccepted {
		t.Fatalf("got %d", got)
	}
	f.added = false
	if got := request(t, r, http.MethodPost, "workflow_run", body, sign(body, "test-secret")).Code; got != http.StatusOK {
		t.Fatalf("dedup got %d", got)
	}
	if f.calls != 2 {
		t.Fatal("queue not called")
	}
}
func TestReceiverSecurityChecks(t *testing.T) {
	body := `{"repository":{"full_name":"example/project"}}`
	r := Receiver{Secret: []byte("test-secret"), Repository: "example/project", Queue: &fakeQueue{}}
	tests := []struct {
		name, method, event, sig string
		want                     int
	}{{"method", http.MethodGet, "ping", sign(body, "test-secret"), 405}, {"signature", http.MethodPost, "ping", "sha256=00", 401}, {"event", http.MethodPost, "push", sign(body, "test-secret"), 422}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := request(t, r, tt.method, tt.event, body, tt.sig)
			if response.Code != tt.want || response.Body.String() != "42\n" {
				t.Fatalf("got status=%d body=%q want status=%d", response.Code, response.Body.String(), tt.want)
			}
		})
	}
}
func TestReceiverPingAndLimits(t *testing.T) {
	body := `{"repository":{"full_name":"example/project"}}`
	r := Receiver{Secret: []byte("s"), Repository: "example/project", Queue: &fakeQueue{}, MaxBody: int64(len(body))}
	response := request(t, r, http.MethodPost, "ping", body, sign(body, "s"))
	if response.Code != http.StatusOK || response.Body.String() != "42\n" {
		t.Fatalf("got status=%d body=%q", response.Code, response.Body.String())
	}
	r.MaxBody = 1
	if got := request(t, r, http.MethodPost, "ping", body, sign(body, "s")).Code; got != 413 {
		t.Fatalf("got %d", got)
	}
}
func TestListenAndServeRejectsNonLoopback(t *testing.T) {
	err := ListenAndServe(context.Background(), "0.0.0.0:4000", http.NotFoundHandler())
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("got %v", err)
	}
}

func TestReceiverQueueFailure(t *testing.T) {
	body := `{"action":"completed","repository":{"full_name":"example/project"},"workflow_run":{"id":42,"head_sha":"0123456789012345678901234567890123456789"}}`
	r := Receiver{Secret: []byte("s"), Repository: "example/project", Queue: &fakeQueue{err: errors.New("down")}}
	if got := request(t, r, http.MethodPost, "workflow_run", body, sign(body, "s")).Code; got != 503 {
		t.Fatalf("got %d", got)
	}
}

type fakeTargetQueue struct {
	fakeQueue
	source, target, repository string
}

func (f *fakeTargetQueue) EnqueueFor(_ context.Context, _ string, source, target, repository string, _ int64, _ string) (bool, error) {
	f.source = source
	f.target = target
	f.repository = repository
	return true, nil
}
func TestReceiverBindsVerifiedRouteToSourceAndTarget(t *testing.T) {
	body := `{"action":"completed","repository":{"full_name":"gnailuy/sudoku"},"workflow_run":{"id":42,"head_sha":"0123456789012345678901234567890123456789"}}`
	q := &fakeTargetQueue{}
	r := Receiver{Secret: []byte("backend-secret"), Repository: "gnailuy/sudoku", SourceID: "backend", TargetID: "sudoku", Queue: q}
	if got := request(t, r, http.MethodPost, "workflow_run", body, sign(body, "backend-secret")).Code; got != http.StatusAccepted {
		t.Fatalf("got %d", got)
	}
	if q.source != "backend" || q.target != "sudoku" || q.repository != "gnailuy/sudoku" {
		t.Fatalf("identity not bound: %+v", q)
	}
}
func TestServiceSelectsSecretByExactPathBeforePayload(t *testing.T) {
	body := `{"repository":{"full_name":"gnailuy/sudoku"}}`
	q := &fakeQueue{}
	s := Service{Routes: map[string]Receiver{"/hooks/backend": {Secret: []byte("backend"), Repository: "gnailuy/sudoku", Queue: q}, "/hooks/frontend": {Secret: []byte("frontend"), Repository: "gnailuy/sudoku-ui", Queue: q}}}
	req := httptest.NewRequest(http.MethodPost, "/hooks/backend", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-GitHub-Delivery", "12345678-1234-1234-1234-123456789abc")
	req.Header.Set("X-Hub-Signature-256", sign(body, "frontend"))
	out := httptest.NewRecorder()
	s.ServeHTTP(out, req)
	if out.Code != http.StatusUnauthorized {
		t.Fatalf("wrong route secret accepted: %d", out.Code)
	}
}
