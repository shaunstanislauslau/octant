package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path"

	"github.com/gorilla/mux"
	"github.com/heptio/developer-dash/internal/cluster"
	"github.com/heptio/developer-dash/internal/log"
	"github.com/heptio/developer-dash/internal/mime"
	"github.com/heptio/developer-dash/internal/module"
	"github.com/heptio/developer-dash/internal/sugarloaf"
)

var (
	// acceptedHosts are the hosts this api will answer for.
	acceptedHosts = []string{
		"localhost",
		"127.0.0.1",
	}
)

func serveAsJSON(w http.ResponseWriter, v interface{}, logger log.Logger) {
	w.Header().Set("Content-Type", mime.JSONContentType)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Errorf("encoding JSON response: %v", err)
	}
}

// Service is an API service.
type Service interface {
	RegisterModule(module.Module) error
	Handler(ctx context.Context) *mux.Router
}

type errorMessage struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type errorResponse struct {
	Error errorMessage `json:"error,omitempty"`
}

// RespondWithError responds with an error message.
func RespondWithError(w http.ResponseWriter, code int, message string, logger log.Logger) {
	r := &errorResponse{
		Error: errorMessage{
			Code:    code,
			Message: message,
		},
	}

	logger.With(
		"code", code,
		"message", message,
	).Infof("unable to serve")

	w.Header().Set("Content-Type", mime.JSONContentType)

	w.WriteHeader(code)

	if err := json.NewEncoder(w).Encode(r); err != nil {
		logger.Errorf("encoding JSON response: %v", err)
	}
}

// API is the API for the dashboard client
type API struct {
	ctx           context.Context
	nsClient      cluster.NamespaceInterface
	infoClient    cluster.InfoInterface
	moduleManager module.ManagerInterface
	prefix        string
	logger        log.Logger

	modulePaths map[string]module.Module
	modules     []module.Module
}

// New creates an instance of API.
func New(ctx context.Context, prefix string, nsClient cluster.NamespaceInterface, infoClient cluster.InfoInterface, moduleManager module.ManagerInterface, logger log.Logger) *API {
	return &API{
		ctx:           ctx,
		prefix:        prefix,
		nsClient:      nsClient,
		infoClient:    infoClient,
		moduleManager: moduleManager,
		modulePaths:   make(map[string]module.Module),
		logger:        logger,
	}
}

// Handler returns a HTTP handler for the service.
func (a *API) Handler(ctx context.Context) *mux.Router {
	router := mux.NewRouter()
	router.Use(rebindHandler(acceptedHosts))

	s := router.PathPrefix(a.prefix).Subrouter()

	namespacesService := newNamespaces(a.nsClient, a.logger)
	s.Handle("/namespaces", namespacesService).Methods(http.MethodGet)

	ans := newAPINavSections(a.modules)

	navigationService := newNavigation(ans, a.logger)
	// Support no namespace (default) or specifying namespace in path
	s.Handle("/navigation", navigationService).Methods(http.MethodGet)
	s.Handle("/navigation/namespace/{namespace}", navigationService).Methods(http.MethodGet)

	namespaceUpdateService := newNamespace(a.moduleManager, a.logger)
	s.HandleFunc("/namespace", namespaceUpdateService.update).Methods(http.MethodPost)
	s.HandleFunc("/namespace", namespaceUpdateService.read).Methods(http.MethodGet)

	infoService := newClusterInfo(a.infoClient, a.logger)
	s.Handle("/cluster-info", infoService)

	// Register content routes
	contentService := &contentHandler{
		nsClient:    a.nsClient,
		modulePaths: a.modulePaths,
		modules:     a.modules,
		logger:      a.logger,
		prefix:      a.prefix,
	}

	if err := contentService.RegisterRoutes(ctx, s); err != nil {
		a.logger.Errorf("register routers: %v", err)
	}

	s.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.logger.Errorf("api handler not found: %s", r.URL.String())
		RespondWithError(w, http.StatusNotFound, "not found", a.logger)
	})

	return router
}

// RegisterModule registers a module with the API service.
func (a *API) RegisterModule(m module.Module) error {
	contentPath := path.Join("/content", m.ContentPath())
	a.logger.With("contentPath", contentPath).Debugf("registering content path")
	a.modulePaths[contentPath] = m
	a.modules = append(a.modules, m)

	return nil
}

type apiNavSections struct {
	modules []module.Module
}

func newAPINavSections(modules []module.Module) *apiNavSections {
	return &apiNavSections{
		modules: modules,
	}
}

func (ans *apiNavSections) Sections(ctx context.Context, namespace string) ([]*sugarloaf.Navigation, error) {
	var sections []*sugarloaf.Navigation

	for _, m := range ans.modules {
		contentPath := path.Join("/content", m.ContentPath())
		nav, err := m.Navigation(ctx, namespace, contentPath)
		if err != nil {
			return nil, err
		}

		sections = append(sections, nav)
	}

	return sections, nil
}
