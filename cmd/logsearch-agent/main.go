package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"logsearch/internal/config"
	"logsearch/internal/search"
	"logsearch/internal/server"
)

func main() {
	configPath := flag.String("config", "configs/agent.yaml", "agent configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	searchService, err := search.New(search.Options{
		Roots:             cfg.Search.Roots,
		AllowedExtensions: cfg.Search.AllowedExtensions,
		PodNameContains:   cfg.Search.PodNameContains,
		ProcessLogs:       processRules(cfg.Search.ProcessLogs),
		MaxFiles:          cfg.Search.MaxFilesPerRequest,
		MaxResults:        cfg.Search.HardMaxResults,
		MaxResponseBytes:  cfg.Search.MaxResponseBytes,
		MaxLineBytes:      cfg.Search.MaxLineBytes,
		MaxMultilineBytes: cfg.Search.MaxMultilineBytes,
		MaxMultilineLines: cfg.Search.MaxMultilineLines,
	})
	if err != nil {
		log.Fatalf("initialize search service: %v", err)
	}

	httpServer := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           server.Handler(cfg, server.New(cfg, searchService)),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("logsearch agent listening on %s node=%s roots=%d", cfg.Server.Listen, cfg.Server.NodeName, len(cfg.Search.Roots))
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

func processRules(configs []config.ProcessLogConfig) []search.ProcessLogRule {
	rules := make([]search.ProcessLogRule, 0, len(configs))
	for _, item := range configs {
		var maxAge time.Duration
		if item.MaxFileAge != "" {
			maxAge, _ = time.ParseDuration(item.MaxFileAge)
		}
		rules = append(rules, search.ProcessLogRule{
			Name: item.Name, CommRegex: item.CommRegex, CmdlineRegex: item.CmdlineRegex,
			IncludeRegex: item.IncludeRegex, ExcludeRegex: item.ExcludeRegex,
			LogDirs: item.LogDirs, FilePatterns: item.FilePatterns,
			MaxFiles: item.MaxFiles, MaxFileAge: maxAge,
			MultilineStartPattern: item.Multiline.StartPattern,
		})
	}
	return rules
}
