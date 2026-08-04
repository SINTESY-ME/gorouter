package httpx

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/jhon/gorouter/internal/app"
	"github.com/jhon/gorouter/internal/domain"
)

const (
	rtkSettingKey                = "rtk_enabled"
	cacheSettingKey              = "cache_enabled"
	semanticCacheSettingKey      = "semantic_cache_enabled"
	semanticCacheModeSettingKey  = "semantic_cache_mode"
	semanticCacheModelSettingKey = "semantic_cache_model"
	hooksSettingKey              = "hooks_enabled"
	cachingGroupsSettingKey      = "caching_groups"
	webhookURLSettingKey         = "webhook_url"
)

// handleGetSettings returns user-configurable gorouter settings (RTK + cache
// toggles). Both persist across restarts via SettingRepo.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	rtk, cache, semCache, semMode, semModel := false, false, false, "active", ""
	if s.Settings != nil {
		if v, err := s.Settings.Get(r.Context(), rtkSettingKey); err == nil {
			rtk = v == "true"
		}
		if v, err := s.Settings.Get(r.Context(), cacheSettingKey); err == nil {
			cache = v == "true"
		}
		if v, err := s.Settings.Get(r.Context(), semanticCacheSettingKey); err == nil {
			semCache = v == "true"
		}
		if v, err := s.Settings.Get(r.Context(), semanticCacheModeSettingKey); err == nil {
			if v == "lazy" || v == "active" {
				semMode = v
			}
		}
		if v, err := s.Settings.Get(r.Context(), semanticCacheModelSettingKey); err == nil {
			semModel = v
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rtk_enabled":            rtk,
		"cache_enabled":          cache,
		"semantic_cache_enabled": semCache,
		"semantic_cache_mode":    semMode,
		"semantic_cache_model":   semModel,
		"hooks_enabled":          s.currentHooks(r),
		"caching_groups":         s.currentCachingGroups(r),
		"webhook_url":            app.CurrentWebhookURL(),
	})
}

// currentCachingGroups returns the caching_groups setting as a model→group map
// (JSON object of name → model list), defaulting to empty.
func (s *Server) currentCachingGroups(r *http.Request) map[string][]string {
	groups := map[string][]string{}
	if s.Settings != nil {
		if v, err := s.Settings.Get(r.Context(), cachingGroupsSettingKey); err == nil && v != "" {
			_ = json.Unmarshal([]byte(v), &groups)
		}
	}
	return groups
}

// currentHooks returns the enabled hook names from settings (JSON array),
// defaulting to an empty list.
func (s *Server) currentHooks(r *http.Request) []string {
	var names []string
	if s.Settings != nil {
		if v, err := s.Settings.Get(r.Context(), hooksSettingKey); err == nil && v != "" {
			_ = json.Unmarshal([]byte(v), &names)
		}
	}
	return names
}

// handleUpdateSettings updates gorouter settings. Both toggles are live —
// the compressor and cache are wired/unwired without a restart.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RTKEnabled           *bool               `json:"rtk_enabled"`
		CacheEnabled         *bool               `json:"cache_enabled"`
		SemanticCacheEnabled *bool               `json:"semantic_cache_enabled"`
		SemanticCacheMode    *string             `json:"semantic_cache_mode"`
		SemanticCacheModel   *string             `json:"semantic_cache_model"`
		HooksEnabled         []string            `json:"hooks_enabled"`
		CachingGroups        map[string][]string `json:"caching_groups"`
		WebhookURL           *string             `json:"webhook_url"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.RTKEnabled != nil {
		if s.Settings != nil {
			if err := s.Settings.Set(r.Context(), rtkSettingKey, strconv.FormatBool(*req.RTKEnabled)); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if *req.RTKEnabled {
			if s.Router.Compressor == nil && s.RTKCompressorFactory != nil {
				s.Router.Compressor = s.RTKCompressorFactory()
			}
		} else {
			s.Router.Compressor = nil
		}
	}
	if req.CacheEnabled != nil {
		if s.Settings != nil {
			if err := s.Settings.Set(r.Context(), cacheSettingKey, strconv.FormatBool(*req.CacheEnabled)); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if *req.CacheEnabled {
			if s.Router.Cache == nil && s.CacheFactory != nil {
				s.Router.Cache = app.NewCacheService(s.CacheFactory())
			}
		} else {
			s.Router.Cache = nil
		}
	}
	if req.SemanticCacheMode != nil {
		if *req.SemanticCacheMode != "active" && *req.SemanticCacheMode != "lazy" {
			writeError(w, http.StatusBadRequest, "semantic_cache_mode must be active or lazy")
			return
		}
		if s.Settings != nil {
			if err := s.Settings.Set(r.Context(), semanticCacheModeSettingKey, *req.SemanticCacheMode); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if s.SemanticCache != nil {
			s.SemanticCache.SetMode(*req.SemanticCacheMode)
		}
	}
	if req.SemanticCacheEnabled != nil {
		if s.Settings != nil {
			if err := s.Settings.Set(r.Context(), semanticCacheSettingKey, strconv.FormatBool(*req.SemanticCacheEnabled)); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if s.SemanticCache != nil {
			s.SemanticCache.SetEnabled(*req.SemanticCacheEnabled)
		}
	}
	if req.SemanticCacheModel != nil {
		if s.Settings != nil {
			if err := s.Settings.Set(r.Context(), semanticCacheModelSettingKey, *req.SemanticCacheModel); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if s.SemanticCache != nil {
			s.SemanticCache.SetModel(*req.SemanticCacheModel)
		}
	}
	// Hooks: absent field leaves hooks untouched; an explicit [] disables
	// them. Rebuilding is validated against the factory first so an unknown
	// hook name is rejected without persisting.
	if req.HooksEnabled != nil {
		if s.HookFactory != nil {
			p, err := s.HookFactory(req.HooksEnabled)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			s.Router.Hooks = p
		}
		if s.Settings != nil {
			b, _ := json.Marshal(req.HooksEnabled)
			if err := s.Settings.Set(r.Context(), hooksSettingKey, string(b)); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	// Caching groups: absent leaves them untouched; an explicit {} disables
	// them. Applied live to the active response cache.
	if req.CachingGroups != nil {
		groups := map[string]string{}
		for name, models := range req.CachingGroups {
			for _, m := range models {
				groups[m] = name
			}
		}
		if s.Router.Cache != nil {
			s.Router.Cache.SetCachingGroups(groups)
		}
		if s.Settings != nil {
			b, _ := json.Marshal(req.CachingGroups)
			if err := s.Settings.Set(r.Context(), cachingGroupsSettingKey, string(b)); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	// Webhook URL: absent leaves it untouched; an explicit "" clears it.
	// Applied live to the active webhook_logging hook.
	if req.WebhookURL != nil {
		app.SetWebhookURL(*req.WebhookURL)
		if s.Settings != nil {
			if err := s.Settings.Set(r.Context(), webhookURLSettingKey, *req.WebhookURL); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated"})
}

var _ domain.SettingRepo
