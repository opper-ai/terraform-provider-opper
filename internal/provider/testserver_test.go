package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

// projectTestAPI exercises the real provider HTTP client through Terraform's
// plugin protocol, without needing a credential or touching an Opper account.
type projectTestAPI struct {
	mu                               sync.Mutex
	projects                         map[string]client.Project
	initialAccess                    map[string]client.InitialProjectAccess
	createFailure, createRequests    int
	sequence                         int
	creates, updates, deletes, reads int
	getFailure, deleteFailure        int
	rejectNamePatch                  bool
	lastPatchFields                  map[string]bool
	server                           *httptest.Server
}

func newProjectTestAPI(t *testing.T) *projectTestAPI {
	t.Helper()
	api := &projectTestAPI{projects: make(map[string]client.Project), initialAccess: make(map[string]client.InitialProjectAccess)}
	api.server = httptest.NewServer(http.HandlerFunc(api.serveHTTP))
	t.Cleanup(api.server.Close)
	return api
}

func (api *projectTestAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	defer api.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer op-mak-local-test" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "unauthenticated"})
		return
	}
	const path = "/management/v1/projects"
	id := strings.TrimPrefix(r.URL.Path, path+"/")
	writeProject := func(code int, p client.Project) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": p})
	}
	notFound := func() {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "project not found"})
	}
	switch {
	case r.Method == http.MethodPost && (r.URL.Path == path || r.URL.Path == path+"/with-access"):
		api.createRequests++
		if api.createFailure != 0 {
			w.WriteHeader(api.createFailure)
			return
		}
		var body struct {
			Name        string                       `json:"name"`
			Description string                       `json:"description"`
			Access      *client.InitialProjectAccess `json:"access"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			http.Error(w, "invalid create body", http.StatusBadRequest)
			return
		}
		if (r.URL.Path == path+"/with-access") != (body.Access != nil) {
			http.Error(w, "initial access requires the atomic endpoint", http.StatusBadRequest)
			return
		}
		for _, p := range api.projects {
			if p.Name == body.Name {
				http.Error(w, "duplicate project", http.StatusConflict)
				return
			}
		}
		api.sequence++
		p := client.Project{UUID: fmt.Sprintf("11111111-1111-4111-8111-%012d", api.sequence), Name: body.Name, Description: body.Description, CreatedAt: "2026-11-03T09:00:00Z"}
		api.projects[p.UUID] = p
		api.initialAccess[p.UUID] = client.InitialProjectAccess{Mode: "organization", RoleKeys: []string{}, UserUUIDs: []string{}}
		if body.Access != nil {
			api.initialAccess[p.UUID] = *body.Access
		}
		api.creates++
		writeProject(http.StatusCreated, p)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, path+"/"):
		api.reads++
		if api.getFailure != 0 {
			w.WriteHeader(api.getFailure)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "access denied"})
			return
		}
		p, ok := api.projects[id]
		if !ok {
			notFound()
			return
		}
		writeProject(http.StatusOK, p)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, path+"/"):
		p, ok := api.projects[id]
		if !ok {
			notFound()
			return
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid patch body", http.StatusBadRequest)
			return
		}
		api.lastPatchFields = make(map[string]bool, len(body))
		for field := range body {
			api.lastPatchFields[field] = true
		}
		if _, hasName := body["name"]; api.rejectNamePatch && p.Name == "default" && hasName {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "cannot rename the default project of the Opper organization"})
			return
		}
		if raw, ok := body["name"]; ok {
			if err := json.Unmarshal(raw, &p.Name); err != nil || strings.TrimSpace(p.Name) == "" {
				http.Error(w, "invalid name", http.StatusBadRequest)
				return
			}
			p.Name = strings.TrimSpace(p.Name)
		}
		if raw, ok := body["description"]; ok {
			if string(raw) == "null" {
				p.Description = ""
			} else if err := json.Unmarshal(raw, &p.Description); err != nil {
				http.Error(w, "invalid description", http.StatusBadRequest)
				return
			}
		}
		if len(body) == 0 {
			http.Error(w, "empty patch", http.StatusBadRequest)
			return
		}
		api.projects[id] = p
		api.updates++
		writeProject(http.StatusOK, p)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, path+"/"):
		if api.deleteFailure != 0 {
			w.WriteHeader(api.deleteFailure)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "access denied"})
			return
		}
		if _, ok := api.projects[id]; !ok {
			notFound()
			return
		}
		delete(api.projects, id)
		delete(api.initialAccess, id)
		api.deletes++
		w.WriteHeader(http.StatusNoContent)
	default:
		notFound()
	}
}

func (api *projectTestAPI) drift(id, name, description string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	p := api.projects[id]
	p.Name, p.Description = name, description
	api.projects[id] = p
}

func (api *projectTestAPI) externalDelete(id string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	delete(api.projects, id)
}
