// Package fakebrowserbase is an in-memory stand-in for the Browserbase
// sessions API. Its sessions all connect to one browser the test provides.
package fakebrowserbase

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sync"
	"testing"
)

// APIKey is the only key the server accepts.
const APIKey = "bb_test_key"

// Session is a session the server created.
type Session struct {
	ID       string
	Status   string
	Timeout  int
	Metadata map[string]any
}

// Server is a fake Browserbase API.
type Server struct {
	URL string
	// ConnectURL is what every session's connectUrl points at: a test's local
	// browser, reached over CDP.
	ConnectURL string

	mu        sync.Mutex
	sessions  []*Session
	limited   int
	unhandled []string
}

// New starts a server, registering cleanup with t.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{ConnectURL: "ws://127.0.0.1:1/devtools/browser/none"}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sessions", s.create)
	mux.HandleFunc("POST /v1/sessions/{id}", s.update)
	mux.HandleFunc("GET /v1/sessions", s.list)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.unhandled = append(s.unhandled, r.Method+" "+r.URL.RequestURI())
		s.mu.Unlock()
		http.Error(w, `{"message":"the fake Browserbase API has no route for this"}`, http.StatusNotFound)
	})
	srv := httptest.NewServer(auth(mux))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// LimitNext refuses the next n session creations with 429, as a plan limit does.
func (s *Server) LimitNext(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limited = n
}

// Sessions returns a copy of every session created so far.
func (s *Server) Sessions() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, 0, len(s.sessions))
	for _, ss := range s.sessions {
		out = append(out, *ss)
	}
	return out
}

// Unhandled returns requests no route matched.
func (s *Server) Unhandled() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.unhandled)
}

func auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-BB-API-Key") != APIKey {
			http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Timeout      int            `json:"timeout"`
		UserMetadata map[string]any `json:"userMetadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"message":"bad body"}`, http.StatusBadRequest)
		return
	}
	// Like Browserbase, refuse an empty metadata value.
	if emptyValue(body.UserMetadata) {
		http.Error(w, `{"message":"Value is not a valid metadata value: "}`, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limited > 0 {
		s.limited--
		w.Header().Set("Retry-After", "1")
		http.Error(w, `{"message":"You've exceeded your max concurrent sessions limit"}`, http.StatusTooManyRequests)
		return
	}
	ss := &Session{ID: fmt.Sprintf("sess_%d", len(s.sessions)+1), Status: "RUNNING", Timeout: body.Timeout, Metadata: body.UserMetadata}
	s.sessions = append(s.sessions, ss)
	writeJSON(w, map[string]any{"id": ss.ID, "connectUrl": s.ConnectURL, "status": ss.Status})
}

func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range s.sessions {
		if ss.ID == r.PathValue("id") {
			if body.Status == "REQUEST_RELEASE" {
				ss.Status = "COMPLETED"
			}
			writeJSON(w, map[string]any{"id": ss.ID, "status": ss.Status})
			return
		}
	}
	http.Error(w, `{"message":"Session not found"}`, http.StatusNotFound)
}

// query matches the one form setup-browserbase sends:
// user_metadata['ci']['run']:'<id>'.
var query = regexp.MustCompile(`^user_metadata\['ci'\]\['run'\]:'([^']*)'$`)

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	run := ""
	if q := r.URL.Query().Get("q"); q != "" {
		m := query.FindStringSubmatch(q)
		if m == nil {
			http.Error(w, `{"message":"unsupported query"}`, http.StatusBadRequest)
			return
		}
		run = m[1]
	}
	status := r.URL.Query().Get("status")
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for _, ss := range s.sessions {
		ci, _ := ss.Metadata["ci"].(map[string]any)
		if run != "" && (ci == nil || ci["run"] != run) {
			continue
		}
		if status != "" && ss.Status != status {
			continue
		}
		out = append(out, map[string]any{"id": ss.ID, "status": ss.Status})
	}
	writeJSON(w, out)
}

func emptyValue(m map[string]any) bool {
	for _, v := range m {
		switch v := v.(type) {
		case string:
			if v == "" {
				return true
			}
		case map[string]any:
			if emptyValue(v) {
				return true
			}
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
