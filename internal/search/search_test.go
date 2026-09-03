package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchAllKeywordsMustMatchSameLine(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "demo-ns_demo-pod_uid", "api")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "before\nrequest-123 unrelated\nrequest-123 payment complete\nafter\n"
	if err := os.WriteFile(filepath.Join(logDir, "0.log"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{
		Roots:             []string{root},
		AllowedExtensions: []string{".log"},
		MaxFiles:          10,
		MaxResults:        10,
		MaxResponseBytes:  1 << 20,
		MaxLineBytes:      1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(context.Background(), Request{
		Keywords:      []string{"request-123", "payment"},
		Mode:          KeywordAll,
		BeforeContext: 1,
		AfterContext:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected one same-line match, got %d", len(result.Matches))
	}
	match := result.Matches[0]
	if match.Namespace != "demo-ns" || match.Pod != "demo-pod" || match.Container != "api" {
		t.Fatalf("unexpected metadata: %#v", match.File)
	}
	if len(match.Before) != 1 || match.Before[0] != "request-123 unrelated" {
		t.Fatalf("unexpected before context: %#v", match.Before)
	}
	if len(match.After) != 1 || match.After[0] != "after" {
		t.Fatalf("unexpected after context: %#v", match.After)
	}
}

func TestLineMatchesAnyIsCaseInsensitive(t *testing.T) {
	if !lineMatches("Trace REQUEST-123", []string{"request-123", "missing"}, KeywordAny, false) {
		t.Fatal("expected case-insensitive ANY match")
	}
	if lineMatches("Trace REQUEST-123", []string{"request-123", "missing"}, KeywordAll, false) {
		t.Fatal("did not expect ALL match when one keyword is absent")
	}
}

func TestAgentPodNameContainsRestrictsFiles(t *testing.T) {
	root := t.TempDir()
	for _, podDir := range []string{"demo-ns_order-api-abc_uid", "demo-ns_payment-api-xyz_uid"} {
		dir := filepath.Join(root, podDir, "api")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "0.log"), []byte("request-123\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	service, err := New(Options{
		Roots:             []string{root},
		AllowedExtensions: []string{".log"},
		PodNameContains:   []string{"ORDER-API"},
		MaxFiles:          10,
		MaxResults:        10,
		MaxResponseBytes:  1 << 20,
		MaxLineBytes:      1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(context.Background(), Request{Keywords: []string{"request-123"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 || result.Matches[0].Pod != "order-api-abc" {
		t.Fatalf("expected only order-api pod, got %#v", result.Matches)
	}
}
