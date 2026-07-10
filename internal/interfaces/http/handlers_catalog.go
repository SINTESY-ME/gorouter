package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jhon/gorouter/internal/providers"
)

// ---- Provider catalog (installed) ----

func (s *Server) handleListCatalog(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, s.Catalog.ListInstalled())
}

func (s *Server) handleGetCatalog(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeError(w, http.StatusNotFound, "catalog unavailable")
		return
	}
	id := chi.URLParam(r, "id")
	def := s.Catalog.Lookup(id)
	if def == nil {
		writeError(w, http.StatusNotFound, "provider not found")
		return
	}
	writeJSON(w, http.StatusOK, def)
}

// ---- Provider store (remote install) ----

func (s *Server) handleListStore(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	entries, err := s.Catalog.ListAvailable()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleInstallStore(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	id := chi.URLParam(r, "id")
	def, err := s.Catalog.Install(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, def)
}

func (s *Server) handleRemoveStore(w http.ResponseWriter, r *http.Request) {
	if s.Catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.Catalog.Remove(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// applyTemplate fills empty connection fields from a catalog template.
func applyTemplate(c *providers.ProviderDef, providerID, baseURL, format, auth, apiKey *string) {
	if c == nil {
		return
	}
	if *providerID == "" {
		*providerID = c.ID
	}
	if *baseURL == "" {
		*baseURL = c.Transport.BaseURL
	}
	if *format == "" || *format == "auto" {
		*format = c.Transport.Format
	}
	if *auth == "" {
		*auth = c.Transport.Auth
		if *auth == "" {
			*auth = "bearer"
		}
	}
	// Free / no-credential templates use a public placeholder key when auth is bearer.
	if c.NoAuth && *apiKey == "" && (*auth == "bearer" || *auth == "") {
		*apiKey = "public"
		*auth = "bearer"
	}
}
