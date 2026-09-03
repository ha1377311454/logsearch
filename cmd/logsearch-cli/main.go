package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	logsearchv1 "logsearch/api/logsearch/v1"
	"logsearch/api/logsearch/v1/logsearchv1connect"
)

func main() {
	serverURL := flag.String("server", "http://127.0.0.1:9000", "agent base URL")
	keywordText := flag.String("keywords", "", "comma-separated keywords")
	mode := flag.String("mode", "all", "keyword mode: all or any")
	token := flag.String("token", "", "Bearer token")
	timeout := flag.Duration("timeout", 20*time.Second, "request timeout")
	maxResults := flag.Int("max-results", 100, "maximum result count")
	before := flag.Int("before", 2, "lines before each match")
	after := flag.Int("after", 2, "lines after each match")
	flag.Parse()

	keywords := split(*keywordText)
	if len(keywords) == 0 {
		fmt.Fprintln(os.Stderr, "-keywords is required")
		os.Exit(2)
	}
	keywordMode := logsearchv1.KeywordMode_KEYWORD_MODE_ALL
	if strings.EqualFold(*mode, "any") {
		keywordMode = logsearchv1.KeywordMode_KEYWORD_MODE_ANY
	}
	client := logsearchv1connect.NewLogSearchServiceClient(http.DefaultClient, *serverURL)
	request := connect.NewRequest(&logsearchv1.SearchRequest{
		Keywords:       keywords,
		KeywordMode:    keywordMode,
		MaxResults:     int32(*maxResults),
		TimeoutSeconds: int32(timeout.Seconds()),
		BeforeContext:  int32(*before),
		AfterContext:   int32(*after),
	})
	if *token != "" {
		request.Header().Set("Authorization", "Bearer "+*token)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	response, err := client.Search(ctx, request)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(response.Msg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func split(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
