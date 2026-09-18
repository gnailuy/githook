package githook

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const DefaultWebhookPath = "/hooks/github"
const queuePath = "/maintenance/queue"

type Service struct {
	WebhookPath string
	Receiver    Receiver
	Routes      map[string]Receiver
	Queue       *Queue
}

func (s Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	webhookPath := s.WebhookPath
	if webhookPath == "" {
		webhookPath = DefaultWebhookPath
	}
	if receiver, ok := s.Routes[r.URL.Path]; ok {
		receiver.ServeHTTP(w, r)
		return
	}
	if len(s.Routes) == 0 && r.URL.Path == webhookPath {
		s.Receiver.ServeHTTP(w, r)
		return
	}
	if s.Queue == nil {
		writeDummy(w, http.StatusNotFound)
		return
	}

	switch {
	case r.URL.Path == queuePath && r.Method == http.MethodGet:
		jobs, err := s.Queue.Pending(r.Context())
		writeQueueResult(w, jobs, err)
	case r.URL.Path == queuePath+"/peek" && r.Method == http.MethodGet:
		job, err := s.Queue.Peek(r.Context())
		if errors.Is(err, ErrQueueEmpty) {
			writeJSON(w, http.StatusOK, map[string]any{"job": nil})
			return
		}
		writeQueueResult(w, job, err)
	case r.URL.Path == queuePath && r.Method == http.MethodDelete:
		count, err := s.Queue.Clear(r.Context(), false)
		writeQueueResult(w, map[string]any{"dropped": count}, err)
	case r.URL.Path == queuePath+"/keep-one" && r.Method == http.MethodPost:
		count, err := s.Queue.Clear(r.Context(), true)
		writeQueueResult(w, map[string]any{"dropped": count}, err)
	case strings.HasPrefix(r.URL.Path, queuePath+"/") && r.Method == http.MethodDelete:
		deliveryID := strings.TrimPrefix(r.URL.Path, queuePath+"/")
		if !deliveryPattern.MatchString(deliveryID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid delivery id"})
			return
		}
		dropped, err := s.Queue.Drop(r.Context(), deliveryID)
		if err == nil && !dropped {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "queued request not found"})
			return
		}
		writeQueueResult(w, map[string]any{"dropped": dropped}, err)
	case strings.HasPrefix(r.URL.Path, queuePath):
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	default:
		writeDummy(w, http.StatusNotFound)
	}
}

func writeQueueResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "queue unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
