package server

import (
	"net/http"

	"github.com/XbLuzk/logicmap/internal/impact"
	"github.com/XbLuzk/logicmap/internal/query"
	"github.com/XbLuzk/logicmap/internal/repo"
	"github.com/XbLuzk/logicmap/internal/webhook"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(repoSvc *repo.RepoService, querySvc *query.QueryService, impactSvc *impact.ImpactService, ghWebhook *webhook.GitHubWebhookHandler) http.Handler {
	r := chi.NewRouter()

	r.Use(RequestID)
	r.Use(middleware.RealIP)
	r.Use(Logging)
	r.Use(Recovery)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	repoHandler := repo.NewHandler(repoSvc)
	r.Mount("/", repoHandler.Routes())

	queryHandler := query.NewQueryHandler(querySvc)
	r.Post("/query", queryHandler.HandleQuery)

	impactHandler := impact.NewHandler(impactSvc)
	r.Post("/impact", impactHandler.HandleImpact)
	r.Get("/impact/config", impactHandler.HandleConfig)

	r.Post("/webhooks/github", ghWebhook.HandlePRWebhook)

	return r
}
