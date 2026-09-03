package search

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type KeywordMode int

const (
	KeywordAll KeywordMode = iota
	KeywordAny
)

type Options struct {
	Roots             []string
	AllowedExtensions []string
	PodNameContains   []string
	ProcessLogs       []ProcessLogRule
	ProcRoot          string
	MaxFiles          int
	MaxResults        int
	MaxResponseBytes  int64
	MaxLineBytes      int
}

type Filter struct {
	Namespaces []string
	Pods       []string
	Containers []string
	Patterns   []string
}

type Request struct {
	Keywords      []string
	Mode          KeywordMode
	CaseSensitive bool
	Filter        Filter
	BeforeContext int
	AfterContext  int
	MaxResults    int
	MaxBytes      int64
	StartTime     time.Time
	EndTime       time.Time
}

type File struct {
	SourceType string
	Rule       string
	Namespace  string
	Pod        string
	Container  string
	Path       string
	OpenPath   string
	Size       int64
	Modified   time.Time
}

type Match struct {
	File
	LineNumber int64
	Timestamp  string
	Text       string
	Before     []string
	After      []string
}

type Result struct {
	Matches          []Match
	ScannedFiles     int
	ScannedBytes     int64
	Truncated        bool
	TruncationReason string
}

type Service struct {
	opts         Options
	roots        []string
	processRules []compiledProcessRule
}

func New(opts Options) (*Service, error) {
	if len(opts.Roots) == 0 {
		return nil, errors.New("at least one log root is required")
	}
	roots := make([]string, 0, len(opts.Roots))
	for _, root := range opts.Roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("resolve log root %q: %w", root, err)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("log root is not a directory: %s", resolved)
		}
		roots = append(roots, resolved)
	}
	if opts.ProcRoot == "" {
		opts.ProcRoot = "/proc"
	}
	processRules, err := compileProcessRules(opts.ProcessLogs)
	if err != nil {
		return nil, err
	}
	return &Service{opts: opts, roots: roots, processRules: processRules}, nil
}

func (s *Service) ListFiles(ctx context.Context, filter Filter, limit int) ([]File, bool, error) {
	if limit <= 0 || limit > s.opts.MaxFiles {
		limit = s.opts.MaxFiles
	}
	files, truncated, err := s.walk(ctx, filter, limit)
	return files, truncated, err
}

func (s *Service) Search(ctx context.Context, req Request) (Result, error) {
	keywords := cleanKeywords(req.Keywords)
	if len(keywords) == 0 {
		return Result{}, errors.New("at least one keyword is required")
	}
	if !req.CaseSensitive {
		for i := range keywords {
			keywords[i] = strings.ToLower(keywords[i])
		}
	}
	maxResults := req.MaxResults
	if maxResults <= 0 || maxResults > s.opts.MaxResults {
		maxResults = s.opts.MaxResults
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 || maxBytes > s.opts.MaxResponseBytes {
		maxBytes = s.opts.MaxResponseBytes
	}
	files, filesTruncated, err := s.walk(ctx, req.Filter, s.opts.MaxFiles)
	if err != nil {
		return Result{}, err
	}
	result := Result{Truncated: filesTruncated}
	if filesTruncated {
		result.TruncationReason = "file limit reached"
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			result.Truncated = true
			result.TruncationReason = "query timeout or cancellation"
			return result, nil
		}
		matches, bytesRead, interrupted, err := s.scanFile(ctx, file, req, keywords, maxResults-len(result.Matches), maxBytes-resultSize(result.Matches))
		result.ScannedFiles++
		result.ScannedBytes += bytesRead
		if err != nil {
			return result, err
		}
		result.Matches = append(result.Matches, matches...)
		if interrupted {
			result.Truncated = true
			result.TruncationReason = "query timeout or cancellation"
			break
		}
		if len(result.Matches) >= maxResults {
			result.Truncated = true
			result.TruncationReason = "result limit reached"
			break
		}
		if resultSize(result.Matches) >= maxBytes {
			result.Truncated = true
			result.TruncationReason = "response byte limit reached"
			break
		}
	}
	return result, nil
}

func (s *Service) walk(ctx context.Context, filter Filter, limit int) ([]File, bool, error) {
	files := make([]File, 0, limit)
	for _, root := range s.roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
				return nil
			}
			if !s.allowedExtension(path) {
				return nil
			}
			meta := metadata(path)
			meta.SourceType = "kubelet"
			meta.OpenPath = path
			if !containsAnyFold(meta.Pod, s.opts.PodNameContains) {
				return nil
			}
			if !matchesFilter(meta, filter) {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			meta.Size = info.Size()
			meta.Modified = info.ModTime()
			files = append(files, meta)
			return nil
		})
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return nil, false, err
		}
		if err != nil {
			return nil, false, err
		}
	}
	processFiles, err := s.discoverProcessFiles(ctx, filter)
	if err != nil {
		return nil, false, err
	}
	files = append(files, processFiles...)
	sort.Slice(files, func(i, j int) bool { return files[i].Modified.After(files[j].Modified) })
	truncated := len(files) > limit
	if truncated {
		files = files[:limit]
	}
	return files, truncated, nil
}

func (s *Service) scanFile(ctx context.Context, file File, req Request, keywords []string, maxResults int, maxBytes int64) ([]Match, int64, bool, error) {
	if maxResults <= 0 || maxBytes <= 0 {
		return nil, 0, false, nil
	}
	openPath := file.OpenPath
	if openPath == "" {
		openPath = file.Path
	}
	f, err := os.Open(openPath)
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()

	type pending struct {
		match     Match
		remaining int
	}
	var completed []Match
	var active []*pending
	var before []string
	var bytesRead int64
	var responseBytes int64
	var lineNumber int64
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), s.opts.MaxLineBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			break
		}
		lineNumber++
		line := scanner.Text()
		bytesRead += int64(len(line) + 1)
		for i := 0; i < len(active); {
			active[i].match.After = append(active[i].match.After, line)
			active[i].remaining--
			if active[i].remaining == 0 {
				responseBytes += matchSize(active[i].match)
				completed = append(completed, active[i].match)
				active = append(active[:i], active[i+1:]...)
				continue
			}
			i++
		}
		if lineMatches(line, keywords, req.Mode, req.CaseSensitive) && inTimeRange(line, req.StartTime, req.EndTime) {
			match := Match{File: file, LineNumber: lineNumber, Timestamp: criTimestamp(line), Text: line, Before: append([]string(nil), before...)}
			if req.AfterContext > 0 {
				active = append(active, &pending{match: match, remaining: req.AfterContext})
			} else {
				responseBytes += matchSize(match)
				completed = append(completed, match)
			}
		}
		if req.BeforeContext > 0 {
			before = append(before, line)
			if len(before) > req.BeforeContext {
				before = before[1:]
			}
		}
		if len(completed)+len(active) >= maxResults || responseBytes >= maxBytes {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return completed, bytesRead, false, fmt.Errorf("scan %s: %w", file.Path, err)
	}
	for _, item := range active {
		if len(completed) >= maxResults || responseBytes >= maxBytes {
			break
		}
		responseBytes += matchSize(item.match)
		completed = append(completed, item.match)
	}
	return completed, bytesRead, ctx.Err() != nil, nil
}

func (s *Service) allowedExtension(path string) bool {
	for _, extension := range s.opts.AllowedExtensions {
		if strings.EqualFold(filepath.Ext(path), extension) {
			return true
		}
	}
	return false
}

func metadata(path string) File {
	clean := filepath.Clean(path)
	containerDir := filepath.Base(filepath.Dir(clean))
	podDir := filepath.Base(filepath.Dir(filepath.Dir(clean)))
	parts := strings.Split(podDir, "_")
	file := File{Path: clean, Container: containerDir}
	if len(parts) >= 2 {
		// kubelet 的 Pod 日志目录格式为 <namespace>_<pod-name>_<pod-uid>。
		file.Namespace = parts[0]
		file.Pod = parts[1]
	}
	return file
}

func matchesFilter(file File, filter Filter) bool {
	return matchesAny(file.Namespace, filter.Namespaces) &&
		matchesAny(file.Pod, filter.Pods) &&
		matchesAny(file.Container, filter.Containers) &&
		matchesPattern(file.Path, filter.Patterns)
}

func matchesAny(value string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		if strings.EqualFold(value, strings.TrimSpace(filter)) {
			return true
		}
	}
	return false
}

// containsAnyFold 是 Agent 配置的强制 Pod 范围约束。多个关键词是 OR 关系；
// 没有配置时允许所有 Pod。它在客户端过滤之前执行，客户端无法绕过。
func containsAnyFold(value string, keywords []string) bool {
	if len(keywords) == 0 {
		return true
	}
	value = strings.ToLower(value)
	hasKeyword := false
	for _, keyword := range keywords {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword == "" {
			continue
		}
		hasKeyword = true
		if strings.Contains(value, keyword) {
			return true
		}
	}
	return !hasKeyword
}

func matchesPattern(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if ok, _ := filepath.Match(pattern, filepath.Base(path)); ok {
			return true
		}
	}
	return false
}

func cleanKeywords(keywords []string) []string {
	result := make([]string, 0, len(keywords))
	for _, keyword := range keywords {
		if keyword = strings.TrimSpace(keyword); keyword != "" {
			result = append(result, keyword)
		}
	}
	return result
}

func lineMatches(line string, keywords []string, mode KeywordMode, caseSensitive bool) bool {
	if !caseSensitive {
		line = strings.ToLower(line)
	}
	if mode == KeywordAny {
		for _, keyword := range keywords {
			if strings.Contains(line, keyword) {
				return true
			}
		}
		return false
	}
	for _, keyword := range keywords {
		if !strings.Contains(line, keyword) {
			return false
		}
	}
	return true
}

func criTimestamp(line string) string {
	field, _, _ := strings.Cut(line, " ")
	if _, err := time.Parse(time.RFC3339Nano, field); err == nil {
		return field
	}
	return ""
}

func inTimeRange(line string, start, end time.Time) bool {
	if start.IsZero() && end.IsZero() {
		return true
	}
	timestamp := criTimestamp(line)
	if timestamp == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return true
	}
	return (start.IsZero() || !t.Before(start)) && (end.IsZero() || !t.After(end))
}

func matchSize(match Match) int64 {
	size := int64(len(match.Text) + len(match.Path))
	for _, line := range match.Before {
		size += int64(len(line))
	}
	for _, line := range match.After {
		size += int64(len(line))
	}
	return size
}

func resultSize(matches []Match) int64 {
	var size int64
	for _, match := range matches {
		size += matchSize(match)
	}
	return size
}
