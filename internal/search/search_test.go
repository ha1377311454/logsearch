package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/process"
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

func TestResolveProcessPathUsesTargetProcessRoot(t *testing.T) {
	procRoot := t.TempDir()
	want := filepath.Join(procRoot, "123", "root", "var", "log", "example-api", "example-api.log")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := resolveProcessPath(procRoot, 123, "/var/log/example-api/example-api.log")
	if !ok || got != want {
		t.Fatalf("resolveProcessPath() = (%q, %v), want (%q, true)", got, ok, want)
	}
}

func TestFilesForProcessFindsRecentRotatedLogs(t *testing.T) {
	procRoot := t.TempDir()
	logDir := filepath.Join(procRoot, "123", "root", "var", "log", "example-api")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for index, name := range []string{"example-api.log", "example-api.1.log", "example-api.2.log"} {
		path := filepath.Join(logDir, name)
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		modified := now.Add(time.Duration(index) * time.Minute)
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{opts: Options{ProcRoot: procRoot}}
	rule := compiledProcessRule{rule: ProcessLogRule{
		Name: "example-api", LogDirs: []string{"/var/log/example-api"},
		FilePatterns: []string{"example-api*.log"}, MaxFiles: 2,
	}}
	files := service.filesForProcess(context.Background(), &process.Process{Pid: 123}, podIdentity{Namespace: "example", Pod: "example-api-abc"}, rule, Filter{})
	if len(files) != 2 {
		t.Fatalf("expected two newest files, got %#v", files)
	}
	if filepath.Base(files[0].Path) != "example-api.2.log" || filepath.Base(files[1].Path) != "example-api.1.log" {
		t.Fatalf("unexpected file order: %#v", files)
	}
	if files[0].SourceType != "process" || files[0].Pod != "example-api-abc" {
		t.Fatalf("unexpected process metadata: %#v", files[0])
	}
}
