package search

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

func TestSearchOversizedLineMatchesBeyondDisplayLimit(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "demo-ns_demo-pod_uid", "api")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("x", 2<<20) + " request-after-one-megabyte\n"
	if err := os.WriteFile(filepath.Join(logDir, "0.log"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{
		Roots:             []string{root},
		AllowedExtensions: []string{".log"},
		MaxFiles:          10,
		MaxResults:        10,
		MaxResponseBytes:  4 << 20,
		MaxLineBytes:      1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(context.Background(), Request{Keywords: []string{"request-after-one-megabyte"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected oversized line to match, got %d matches", len(result.Matches))
	}
	if !strings.HasSuffix(result.Matches[0].Text, "[line truncated]") {
		t.Fatalf("expected oversized response line to be marked truncated, got %q", result.Matches[0].Text)
	}
}

func TestSearchUnlimitedLineReturnsCompleteText(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "demo-ns_demo-pod_uid", "api")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 2<<20) + " complete-request"
	if err := os.WriteFile(filepath.Join(logDir, "0.log"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{
		Roots:             []string{root},
		AllowedExtensions: []string{".log"},
		MaxFiles:          10,
		MaxResults:        10,
		MaxResponseBytes:  -1,
		MaxLineBytes:      -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(context.Background(), Request{Keywords: []string{"complete-request"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected one complete oversized line, got %d matches", len(result.Matches))
	}
	if result.Matches[0].Text != line {
		t.Fatalf("expected complete oversized line, got %d of %d bytes", len(result.Matches[0].Text), len(line))
	}
}

func TestScanFileMergesMultilineBeforeKeywordMatching(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "application.log")
	content := strings.Join([]string{
		"2026-09-04 10:00:00.001 ERROR request-123 failed",
		"java.lang.IllegalStateException: payment unavailable",
		"\tat example.Service.call(Service.java:42)",
		"2026-09-04 10:00:01.002 INFO next record",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	service := &Service{opts: Options{
		MaxLineBytes:      1 << 20,
		MaxMultilineBytes: 4 << 20,
		MaxMultilineLines: 1000,
	}}
	file := File{Path: path, OpenPath: path, multilineStart: regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2} `)}
	matches, _, _, err := service.scanFile(context.Background(), file, Request{Mode: KeywordAll}, []string{"request-123", "payment unavailable"}, 10, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one merged match, got %d", len(matches))
	}
	want := strings.Join(strings.Split(strings.TrimSuffix(content, "\n"), "\n")[:3], "\n")
	if matches[0].Text != want {
		t.Fatalf("unexpected merged log\ngot:  %q\nwant: %q", matches[0].Text, want)
	}
	if matches[0].LineNumber != 1 {
		t.Fatalf("expected merged record to start at physical line 1, got %d", matches[0].LineNumber)
	}
}

func TestCompileProcessRulesRejectsInvalidMultilinePattern(t *testing.T) {
	_, err := compileProcessRules([]ProcessLogRule{{Name: "example", MultilineStartPattern: "["}})
	if err == nil || !strings.Contains(err.Error(), "multiline.start_pattern") {
		t.Fatalf("expected multiline pattern error, got %v", err)
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
