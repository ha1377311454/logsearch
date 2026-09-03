package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	logsearchv1 "logsearch/api/logsearch/v1"
	"logsearch/api/logsearch/v1/logsearchv1connect"
	"logsearch/internal/config"
	"logsearch/internal/search"
)

type Service struct {
	logsearchv1connect.UnimplementedLogSearchServiceHandler
	nodeName          string
	search            *search.Service
	defaultTimeout    time.Duration
	hardTimeout       time.Duration
	defaultMaxResults int
	semaphore         chan struct{}
}

func New(cfg config.Config, searchService *search.Service) *Service {
	return &Service{
		nodeName:          cfg.Server.NodeName,
		search:            searchService,
		defaultTimeout:    cfg.DefaultTimeout(),
		hardTimeout:       cfg.HardTimeout(),
		defaultMaxResults: cfg.Search.DefaultMaxResults,
		semaphore:         make(chan struct{}, cfg.Search.MaxConcurrentSearches),
	}
}

func (s *Service) Health(_ context.Context, _ *connect.Request[logsearchv1.HealthRequest]) (*connect.Response[logsearchv1.HealthResponse], error) {
	return connect.NewResponse(&logsearchv1.HealthResponse{Status: "ok", NodeName: s.nodeName}), nil
}

func (s *Service) ListLogFiles(ctx context.Context, req *connect.Request[logsearchv1.ListLogFilesRequest]) (*connect.Response[logsearchv1.ListLogFilesResponse], error) {
	files, truncated, err := s.search.ListFiles(ctx, toFilter(req.Msg), int(req.Msg.Limit))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	response := &logsearchv1.ListLogFilesResponse{Truncated: truncated, Files: make([]*logsearchv1.LogFile, 0, len(files))}
	for _, file := range files {
		response.Files = append(response.Files, &logsearchv1.LogFile{
			Namespace:  file.Namespace,
			Pod:        file.Pod,
			Container:  file.Container,
			Path:       file.Path,
			Size:       file.Size,
			ModifiedAt: file.Modified.Format(time.RFC3339Nano),
			SourceType: file.SourceType,
			SourceRule: file.Rule,
		})
	}
	return connect.NewResponse(response), nil
}

func (s *Service) Search(ctx context.Context, req *connect.Request[logsearchv1.SearchRequest]) (*connect.Response[logsearchv1.SearchResponse], error) {
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	default:
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("too many concurrent searches"))
	}

	timeout := s.defaultTimeout
	if req.Msg.TimeoutSeconds > 0 {
		timeout = time.Duration(req.Msg.TimeoutSeconds) * time.Second
	}
	if timeout > s.hardTimeout {
		timeout = s.hardTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startTime, err := parseTime(req.Msg.StartTime)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid start_time"))
	}
	endTime, err := parseTime(req.Msg.EndTime)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid end_time"))
	}
	started := time.Now()
	maxResults := int(req.Msg.MaxResults)
	if maxResults <= 0 {
		maxResults = s.defaultMaxResults
	}
	result, err := s.search.Search(ctx, search.Request{
		Keywords:      req.Msg.Keywords,
		Mode:          keywordMode(req.Msg.KeywordMode),
		CaseSensitive: req.Msg.CaseSensitive,
		Filter:        toFilter(req.Msg),
		BeforeContext: clamp(int(req.Msg.BeforeContext), 0, 100),
		AfterContext:  clamp(int(req.Msg.AfterContext), 0, 100),
		MaxResults:    maxResults,
		MaxBytes:      req.Msg.MaxBytes,
		StartTime:     startTime,
		EndTime:       endTime,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	response := &logsearchv1.SearchResponse{
		ScannedFiles:     int32(result.ScannedFiles),
		ScannedBytes:     result.ScannedBytes,
		Truncated:        result.Truncated,
		TruncationReason: result.TruncationReason,
		ElapsedMs:        time.Since(started).Milliseconds(),
		Matches:          make([]*logsearchv1.LogMatch, 0, len(result.Matches)),
	}
	for _, match := range result.Matches {
		response.Matches = append(response.Matches, &logsearchv1.LogMatch{
			NodeName:   s.nodeName,
			Namespace:  match.Namespace,
			Pod:        match.Pod,
			Container:  match.Container,
			File:       match.Path,
			LineNumber: match.LineNumber,
			Timestamp:  match.Timestamp,
			Text:       match.Text,
			Before:     match.Before,
			After:      match.After,
			SourceType: match.SourceType,
			SourceRule: match.Rule,
		})
	}
	return connect.NewResponse(response), nil
}

type filterMessage interface {
	GetNamespaces() []string
	GetPods() []string
	GetContainers() []string
	GetFilePatterns() []string
}

func toFilter(message filterMessage) search.Filter {
	return search.Filter{Namespaces: message.GetNamespaces(), Pods: message.GetPods(), Containers: message.GetContainers(), Patterns: message.GetFilePatterns()}
}

func keywordMode(mode logsearchv1.KeywordMode) search.KeywordMode {
	if mode == logsearchv1.KeywordMode_KEYWORD_MODE_ANY {
		return search.KeywordAny
	}
	return search.KeywordAll
}

func parseTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func Handler(cfg config.Config, service *Service) http.Handler {
	path, rpcHandler := logsearchv1connect.NewLogSearchServiceHandler(service)
	mux := http.NewServeMux()
	mux.Handle(path, rpcHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	handler := auth(cfg.Security.Token, mux)
	return cors(cfg.Security.AllowedOrigins, handler)
}

func auth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func cors(origins []string, next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		_, exact := allowed[origin]
		_, wildcard := allowed["*"]
		if origin != "" && (exact || wildcard) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,Connect-Protocol-Version,Connect-Timeout-Ms")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
			w.Header().Set("Access-Control-Expose-Headers", "Grpc-Status,Grpc-Message")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
