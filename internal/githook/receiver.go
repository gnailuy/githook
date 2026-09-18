package githook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strings"
)

const DefaultMaxBody = int64(1 << 20)

var deliveryPattern = regexp.MustCompile(`^[0-9a-fA-F-]{16,64}$`)

type Enqueuer interface {
	Enqueue(context.Context, string, int64, string) (bool, error)
}

type TargetEnqueuer interface {
	EnqueueFor(context.Context, string, string, string, string, int64, string) (bool, error)
}

type Receiver struct {
	Secret     []byte
	Repository string
	SourceID   string
	TargetID   string
	Queue      Enqueuer
	MaxBody    int64
}

type eventEnvelope struct {
	Action     string `json:"action"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	WorkflowRun struct {
		ID      int64  `json:"id"`
		HeadSHA string `json:"head_sha"`
	} `json:"workflow_run"`
}

func (r Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeDummy(w, http.StatusMethodNotAllowed)
		return
	}
	mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeDummy(w, http.StatusUnsupportedMediaType)
		return
	}
	max := r.MaxBody
	if max <= 0 {
		max = DefaultMaxBody
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, max+1))
	if err != nil || int64(len(body)) > max {
		writeDummy(w, http.StatusRequestEntityTooLarge)
		return
	}
	if !validSignature(body, req.Header.Get("X-Hub-Signature-256"), r.Secret) {
		writeDummy(w, http.StatusUnauthorized)
		return
	}
	delivery := req.Header.Get("X-GitHub-Delivery")
	if !deliveryPattern.MatchString(delivery) {
		writeDummy(w, http.StatusBadRequest)
		return
	}
	var event eventEnvelope
	if err := json.Unmarshal(body, &event); err != nil {
		writeDummy(w, http.StatusBadRequest)
		return
	}
	if event.Repository.FullName != r.Repository {
		writeDummy(w, http.StatusForbidden)
		return
	}
	switch req.Header.Get("X-GitHub-Event") {
	case "ping":
		writeDummy(w, http.StatusOK)
	case "workflow_run":
		if event.Action != "completed" || event.WorkflowRun.ID <= 0 || !validSHA(event.WorkflowRun.HeadSHA) {
			writeDummy(w, http.StatusUnprocessableEntity)
			return
		}
		var added bool
		if targeted, ok := r.Queue.(TargetEnqueuer); ok && r.SourceID != "" && r.TargetID != "" {
			added, err = targeted.EnqueueFor(req.Context(), delivery, r.SourceID, r.TargetID, r.Repository, event.WorkflowRun.ID, strings.ToLower(event.WorkflowRun.HeadSHA))
		} else {
			added, err = r.Queue.Enqueue(req.Context(), delivery, event.WorkflowRun.ID, strings.ToLower(event.WorkflowRun.HeadSHA))
		}
		if err != nil {
			writeDummy(w, http.StatusServiceUnavailable)
			return
		}
		if added {
			writeDummy(w, http.StatusAccepted)
		} else {
			writeDummy(w, http.StatusOK)
		}
	default:
		writeDummy(w, http.StatusUnprocessableEntity)
	}
}

func writeDummy(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "42\n")
}

func validSignature(body []byte, header string, secret []byte) bool {
	if len(secret) == 0 || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil || len(want) != sha256.Size {
		return false
	}
	m := hmac.New(sha256.New, secret)
	_, _ = m.Write(body)
	return hmac.Equal(m.Sum(nil), want)
}

func validSHA(s string) bool { _, err := hex.DecodeString(s); return len(s) == 40 && err == nil }

func ListenAndServe(ctx context.Context, addr string, h http.Handler) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address must use a loopback IP")
	}
	s := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5e9, ReadTimeout: 10e9, WriteTimeout: 10e9, IdleTimeout: 30e9, MaxHeaderBytes: 16 << 10}
	go func() { <-ctx.Done(); _ = s.Shutdown(context.Background()) }()
	err = s.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve: %w", err)
}
