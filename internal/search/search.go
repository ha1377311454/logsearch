package search

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
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
	MaxParallelFiles  int
	MaxResults        int
	MaxResponseBytes  int64
	MaxLineBytes      int
	MaxMultilineBytes int
	MaxMultilineLines int
	Debug             bool
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
	QueryID       uint64
}

type File struct {
	SourceType     string
	Rule           string
	Namespace      string
	Pod            string
	Container      string
	Path           string
	OpenPath       string
	Size           int64
	Modified       time.Time
	multilineStart *regexp.Regexp
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
	DiscoveredFiles  int
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
	files, truncated, err := s.walk(ctx, filter, limit, 0)
	return files, truncated, err
}

func (s *Service) Search(ctx context.Context, req Request) (Result, error) {
	totalStarted := time.Now()
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
	if s.opts.MaxResponseBytes < 0 {
		if maxBytes <= 0 {
			maxBytes = int64(^uint64(0) >> 1)
		}
	} else if maxBytes <= 0 || maxBytes > s.opts.MaxResponseBytes {
		maxBytes = s.opts.MaxResponseBytes
	}
	discoveryStarted := time.Now()
	files, filesTruncated, err := s.walk(ctx, req.Filter, s.opts.MaxFiles, req.QueryID)
	if err != nil {
		return Result{}, err
	}
	s.debugf(req.QueryID, "phase=discovery_complete files=%d elapsed_ms=%d", len(files), time.Since(discoveryStarted).Milliseconds())
	result := Result{Truncated: filesTruncated}
	defer func() {
		s.debugf(req.QueryID, "phase=search_complete discovered_files=%d scanned_files=%d scanned_bytes=%d matches=%d truncated=%t reason=%q elapsed_ms=%d",
			result.DiscoveredFiles, result.ScannedFiles, result.ScannedBytes, len(result.Matches), result.Truncated, result.TruncationReason, time.Since(totalStarted).Milliseconds())
	}()
	var responseSize int64
	result.DiscoveredFiles = len(files)
	if filesTruncated {
		result.TruncationReason = "file limit reached"
	}
	parallelism := s.opts.MaxParallelFiles
	if parallelism <= 0 {
		parallelism = 1
	}
	for batchStart := 0; batchStart < len(files); batchStart += parallelism {
		batchStarted := time.Now()
		if err := ctx.Err(); err != nil {
			result.Truncated = true
			result.TruncationReason = "query timeout or cancellation"
			return result, nil
		}
		batchEnd := min(batchStart+parallelism, len(files))
		type scanResult struct {
			matches     []Match
			bytesRead   int64
			interrupted bool
			err         error
		}
		results := make([]scanResult, batchEnd-batchStart)
		var wg sync.WaitGroup
		remainingResults := maxResults - len(result.Matches)
		remainingBytes := maxBytes - responseSize
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				file := files[batchStart+i]
				started := time.Now()
				results[i].matches, results[i].bytesRead, results[i].interrupted, results[i].err = s.scanFile(
					ctx, file, req, keywords, remainingResults, remainingBytes,
				)
				elapsed := time.Since(started)
				s.debugf(req.QueryID, "phase=file_scan source=%s rule=%s path=%q scan_mode=%s fast_path=%t file_bytes=%d scanned_bytes=%d matches=%d elapsed_ms=%d throughput_mb_s=%.2f interrupted=%t error=%t",
					file.SourceType, file.Rule, file.Path, scanMode(file, req, len(keywords)), scanMode(file, req, len(keywords)) != "legacy", file.Size, results[i].bytesRead, len(results[i].matches), elapsed.Milliseconds(), throughputMB(results[i].bytesRead, elapsed), results[i].interrupted, results[i].err != nil)
			}(i)
		}
		wg.Wait()
		s.debugf(req.QueryID, "phase=batch_scan batch_start=%d files=%d elapsed_ms=%d", batchStart, len(results), time.Since(batchStarted).Milliseconds())
		for _, scanned := range results {
			result.ScannedFiles++
			result.ScannedBytes += scanned.bytesRead
			if scanned.err != nil {
				return result, scanned.err
			}
			for _, match := range scanned.matches {
				matchBytes := matchSize(match)
				if len(result.Matches) >= maxResults || responseSize+matchBytes > maxBytes {
					result.Truncated = true
					if len(result.Matches) >= maxResults {
						result.TruncationReason = "result limit reached"
					} else {
						result.TruncationReason = "response byte limit reached"
					}
					return result, nil
				}
				result.Matches = append(result.Matches, match)
				responseSize += matchBytes
			}
			if scanned.interrupted {
				result.Truncated = true
				result.TruncationReason = "query timeout or cancellation"
				return result, nil
			}
		}
		if len(result.Matches) >= maxResults {
			result.Truncated = true
			result.TruncationReason = "result limit reached"
			return result, nil
		}
		if responseSize >= maxBytes {
			result.Truncated = true
			result.TruncationReason = "response byte limit reached"
			return result, nil
		}
	}
	return result, nil
}

func (s *Service) walk(ctx context.Context, filter Filter, limit int, queryID uint64) ([]File, bool, error) {
	files := make([]File, 0, limit)
	for _, root := range s.roots {
		rootStarted := time.Now()
		before := len(files)
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
		s.debugf(queryID, "phase=kubelet_discovery root=%q files=%d elapsed_ms=%d", root, len(files)-before, time.Since(rootStarted).Milliseconds())
	}
	processStarted := time.Now()
	processFiles, err := s.discoverProcessFiles(ctx, filter)
	if err != nil {
		return nil, false, err
	}
	s.debugf(queryID, "phase=process_discovery files=%d elapsed_ms=%d", len(processFiles), time.Since(processStarted).Milliseconds())
	files = append(files, processFiles...)
	sort.Slice(files, func(i, j int) bool { return files[i].Modified.After(files[j].Modified) })
	truncated := len(files) > limit
	if truncated {
		files = files[:limit]
	}
	return files, truncated, nil
}

func (s *Service) debugf(queryID uint64, format string, args ...any) {
	if !s.opts.Debug || queryID == 0 {
		return
	}
	log.Printf("[DEBUG-PERF] query=%d %s", queryID, fmt.Sprintf(format, args...))
}

func throughputMB(bytesRead int64, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(bytesRead) / (1024 * 1024) / elapsed.Seconds()
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
	if usesSingleLineFastPath(file, req) {
		return s.scanSingleLineFast(ctx, bufio.NewReaderSize(f, 64*1024), file, newKeywordMatcher(keywords, req.Mode, req.CaseSensitive), maxResults, maxBytes)
	}
	if usesMultilineOffsetFastPath(file, req, len(keywords)) {
		return s.scanMultilineOffsetFast(ctx, f, file, req, newKeywordMatcher(keywords, req.Mode, req.CaseSensitive), maxResults, maxBytes)
	}

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
	records := &logRecordReader{
		ctx: ctx, reader: bufio.NewReaderSize(f, 64*1024), matcher: newKeywordMatcher(keywords, req.Mode, req.CaseSensitive),
		mode: req.Mode, caseSensitive: req.CaseSensitive, maxDisplayBytes: s.opts.MaxLineBytes,
		start: file.multilineStart, maxMultilineBytes: s.opts.MaxMultilineBytes, maxMultilineLines: s.opts.MaxMultilineLines,
	}
	for {
		if err := ctx.Err(); err != nil {
			break
		}
		record, err := records.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return completed, bytesRead, false, fmt.Errorf("scan %s: %w", file.Path, err)
		}
		lineNumber = record.lineNumber
		line := record.text
		bytesRead += record.bytes
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
		if record.matched && inTimeRange(line, req.StartTime, req.EndTime) {
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
	for _, item := range active {
		if len(completed) >= maxResults || responseBytes >= maxBytes {
			break
		}
		responseBytes += matchSize(item.match)
		completed = append(completed, item.match)
	}
	return completed, bytesRead, ctx.Err() != nil, nil
}

func usesSingleLineFastPath(file File, req Request) bool {
	return file.multilineStart == nil && req.BeforeContext == 0 && req.AfterContext == 0 && req.StartTime.IsZero() && req.EndTime.IsZero()
}

func usesMultilineOffsetFastPath(file File, req Request, keywordCount int) bool {
	return file.multilineStart != nil && strings.HasPrefix(file.multilineStart.String(), "^") &&
		req.BeforeContext == 0 && req.AfterContext == 0 && keywordCount > 0 && keywordCount <= 64
}

func scanMode(file File, req Request, keywordCount int) string {
	if usesSingleLineFastPath(file, req) {
		return "single_line_fast"
	}
	if usesMultilineOffsetFastPath(file, req, keywordCount) {
		return "multiline_offset_fast"
	}
	return "legacy"
}

type offsetRecord struct {
	start, end int64
	lineNumber int64
	bytes      int64
	lines      int
	hits       uint64
}

// scanMultilineOffsetFast 扫描时只保存逻辑记录的文件偏移和关键词位图；
// 未命中记录不构造字符串，命中后才通过 ReadAt 读取需要返回的文本。
func (s *Service) scanMultilineOffsetFast(ctx context.Context, f *os.File, file File, req Request, matcher *keywordMatcher, maxResults int, maxBytes int64) ([]Match, int64, bool, error) {
	reader := bufio.NewReaderSize(f, 64*1024)
	var matches []Match
	var current offsetRecord
	var hasCurrent bool
	var bytesRead, responseBytes, lineNumber int64
	expectedHits := keywordMask(len(matcher.keywords))

	materialize := func(record offsetRecord) (bool, error) {
		if !keywordMaskMatches(record.hits, expectedHits, matcher.mode) {
			return false, nil
		}
		text, err := readRecordAt(f, record, s.opts.MaxLineBytes)
		if err != nil {
			return false, err
		}
		if !inTimeRange(text, req.StartTime, req.EndTime) {
			return false, nil
		}
		match := Match{File: file, LineNumber: record.lineNumber, Timestamp: criTimestamp(text), Text: text}
		responseBytes += matchSize(match)
		matches = append(matches, match)
		return len(matches) >= maxResults || responseBytes >= maxBytes, nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return matches, bytesRead, true, nil
		}
		lineStart := bytesRead
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) == 0 && errors.Is(err, io.EOF) {
			if hasCurrent {
				_, materializeErr := materialize(current)
				return matches, bytesRead, false, materializeErr
			}
			return matches, bytesRead, false, nil
		}
		lineNumber++
		boundary := file.multilineStart.Match(fragment)
		lineBytes := int64(len(fragment))
		lineHits := matcher.mask(fragment)
		var overlap []byte
		if errors.Is(err, bufio.ErrBufferFull) {
			overlap = matcher.fragmentTail(overlap, fragment)
		}
		for errors.Is(err, bufio.ErrBufferFull) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return matches, bytesRead + lineBytes, true, nil
			}
			next, nextErr := reader.ReadSlice('\n')
			lineHits |= matcher.maskWithOverlap(next, &overlap)
			lineBytes += int64(len(next))
			fragment, err = next, nextErr
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return matches, bytesRead + lineBytes, false, err
		}
		lineEnd := lineStart + lineBytes
		bytesRead = lineEnd

		overBytes := hasCurrent && s.opts.MaxMultilineBytes > 0 && current.bytes+lineBytes > int64(s.opts.MaxMultilineBytes)
		overLines := hasCurrent && s.opts.MaxMultilineLines > 0 && current.lines >= s.opts.MaxMultilineLines
		if hasCurrent && (boundary || overBytes || overLines) {
			stop, materializeErr := materialize(current)
			if materializeErr != nil {
				return matches, bytesRead, false, materializeErr
			}
			if stop {
				return matches, bytesRead, false, nil
			}
			hasCurrent = false
		}
		if !hasCurrent {
			current = offsetRecord{start: lineStart, lineNumber: lineNumber}
			hasCurrent = true
		}
		current.end = lineEnd
		current.bytes += lineBytes
		current.lines++
		current.hits |= lineHits

		if errors.Is(err, io.EOF) {
			_, materializeErr := materialize(current)
			return matches, bytesRead, false, materializeErr
		}
	}
}

func keywordMask(count int) uint64 {
	if count == 64 {
		return ^uint64(0)
	}
	return uint64(1)<<count - 1
}

func keywordMaskMatches(hits, expected uint64, mode KeywordMode) bool {
	if mode == KeywordAny {
		return hits != 0
	}
	return hits == expected
}

func readRecordAt(f *os.File, record offsetRecord, maxLineBytes int) (string, error) {
	data := make([]byte, record.end-record.start)
	if _, err := f.ReadAt(data, record.start); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	endedWithNewline := len(data) > 0 && data[len(data)-1] == '\n'
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	texts := make([]string, 0, len(lines))
	for i, line := range lines {
		lineBytes := int64(len(line))
		if i < len(lines)-1 || endedWithNewline {
			lineBytes++
		}
		texts = append(texts, displayLine(line, lineBytes, maxLineBytes))
	}
	return strings.Join(texts, "\n"), nil
}

// scanSingleLineFast 针对无多行、无上下文、无时间过滤的常见查询，直接在
// bufio.Reader 返回的字节上匹配。未命中的普通行不创建展示缓冲区和字符串。
func (s *Service) scanSingleLineFast(ctx context.Context, reader *bufio.Reader, file File, matcher *keywordMatcher, maxResults int, maxBytes int64) ([]Match, int64, bool, error) {
	var matches []Match
	var bytesRead int64
	var responseBytes int64
	var lineNumber int64
	for {
		if err := ctx.Err(); err != nil {
			return matches, bytesRead, true, nil
		}
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) == 0 && errors.Is(err, io.EOF) {
			return matches, bytesRead, false, nil
		}
		lineNumber++
		bytesRead += int64(len(fragment))

		var line physicalLine
		if errors.Is(err, bufio.ErrBufferFull) {
			line, err = readLongPhysicalLine(ctx, reader, matcher, s.opts.MaxLineBytes, fragment)
			bytesRead += line.bytes - int64(len(fragment))
		} else {
			line = physicalLine{bytes: int64(len(fragment)), matched: matcher.matches(fragment)}
			if line.matched {
				line.text = displayLine(fragment, line.bytes, s.opts.MaxLineBytes)
			}
		}
		if err != nil && !errors.Is(err, io.EOF) {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return matches, bytesRead, true, nil
			}
			return matches, bytesRead, false, err
		}
		if line.matched {
			match := Match{File: file, LineNumber: lineNumber, Timestamp: criTimestamp(line.text), Text: line.text}
			responseBytes += matchSize(match)
			matches = append(matches, match)
			if len(matches) >= maxResults || responseBytes >= maxBytes {
				return matches, bytesRead, false, nil
			}
		}
		if errors.Is(err, io.EOF) {
			return matches, bytesRead, false, nil
		}
	}
}

func readLongPhysicalLine(ctx context.Context, reader *bufio.Reader, matcher *keywordMatcher, maxDisplayBytes int, first []byte) (physicalLine, error) {
	unlimitedDisplay := maxDisplayBytes < 0
	if maxDisplayBytes == 0 {
		maxDisplayBytes = 1 << 20
	}
	displayCapacity := 64 * 1024
	if !unlimitedDisplay {
		displayCapacity = min(maxDisplayBytes, displayCapacity)
	}
	display := make([]byte, 0, displayCapacity)
	hits := make([]bool, len(matcher.keywords))
	var overlap []byte
	var bytesRead int64
	consume := func(fragment []byte) {
		bytesRead += int64(len(fragment))
		if unlimitedDisplay {
			display = append(display, fragment...)
		} else if remaining := maxDisplayBytes - len(display); remaining > 0 {
			display = append(display, fragment[:min(remaining, len(fragment))]...)
		}
		overlap = matcher.updateHits(hits, overlap, fragment)
	}
	consume(first)
	for {
		if err := ctx.Err(); err != nil {
			return physicalLine{bytes: bytesRead}, err
		}
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			consume(fragment)
		}
		switch {
		case err == nil:
			return finishPhysicalLine(display, bytesRead, maxDisplayBytes, unlimitedDisplay, hits, matcher.mode), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return finishPhysicalLine(display, bytesRead, maxDisplayBytes, unlimitedDisplay, hits, matcher.mode), io.EOF
		default:
			return physicalLine{bytes: bytesRead}, err
		}
	}
}

func displayLine(line []byte, bytesRead int64, maxDisplayBytes int) string {
	unlimited := maxDisplayBytes < 0
	if maxDisplayBytes == 0 {
		maxDisplayBytes = 1 << 20
	}
	if !unlimited && len(line) > maxDisplayBytes {
		line = line[:maxDisplayBytes]
	}
	text := string(bytesTrimLineEnding(line))
	if !unlimited && bytesRead > int64(maxDisplayBytes) {
		text += " ... [line truncated]"
	}
	return text
}

type physicalLine struct {
	text    string
	bytes   int64
	matched bool
	hits    []bool
}

type logicalRecord struct {
	physicalLine
	lineNumber int64
}

type logRecordReader struct {
	ctx                                  context.Context
	reader                               *bufio.Reader
	matcher                              *keywordMatcher
	mode                                 KeywordMode
	caseSensitive                        bool
	maxDisplayBytes                      int
	start                                *regexp.Regexp
	maxMultilineBytes, maxMultilineLines int
	pending                              *logicalRecord
	physicalLineNumber                   int64
}

func (r *logRecordReader) next() (logicalRecord, error) {
	first, err := r.nextPhysical()
	if err != nil {
		return logicalRecord{}, err
	}
	if r.start == nil {
		return first, nil
	}

	parts := []string{first.text}
	hits := append([]bool(nil), first.hits...)
	bytesRead := first.bytes
	lines := 1
	for {
		next, err := r.nextPhysical()
		if errors.Is(err, io.EOF) {
			return mergedRecord(first.lineNumber, parts, hits, bytesRead, r.mode), nil
		}
		if err != nil {
			return logicalRecord{}, err
		}
		overBytes := r.maxMultilineBytes > 0 && bytesRead+next.bytes > int64(r.maxMultilineBytes)
		overLines := r.maxMultilineLines > 0 && lines >= r.maxMultilineLines
		if r.start.MatchString(next.text) || overBytes || overLines {
			r.pending = &next
			return mergedRecord(first.lineNumber, parts, hits, bytesRead, r.mode), nil
		}
		parts = append(parts, next.text)
		bytesRead += next.bytes
		lines++
		for i := range hits {
			hits[i] = hits[i] || next.hits[i]
		}
	}
}

func (r *logRecordReader) nextPhysical() (logicalRecord, error) {
	if r.pending != nil {
		line := *r.pending
		r.pending = nil
		return line, nil
	}
	line, err := readPhysicalLineWithMatcher(r.ctx, r.reader, r.matcher, r.maxDisplayBytes)
	if err != nil {
		return logicalRecord{}, err
	}
	r.physicalLineNumber++
	return logicalRecord{physicalLine: line, lineNumber: r.physicalLineNumber}, nil
}

func mergedRecord(lineNumber int64, parts []string, hits []bool, bytesRead int64, mode KeywordMode) logicalRecord {
	return logicalRecord{
		physicalLine: physicalLine{text: strings.Join(parts, "\n"), bytes: bytesRead, matched: keywordHitsMatch(hits, mode), hits: hits},
		lineNumber:   lineNumber,
	}
}

// readPhysicalLine 分片读取一条物理行，避免超长 JSON 日志触发 bufio.Scanner 的 token too long。
// maxDisplayBytes 仅限制返回给客户端的文本；关键词匹配仍覆盖整条物理行。
func readPhysicalLine(ctx context.Context, reader *bufio.Reader, keywords []string, mode KeywordMode, caseSensitive bool, maxDisplayBytes int) (physicalLine, error) {
	return readPhysicalLineWithMatcher(ctx, reader, newKeywordMatcher(keywords, mode, caseSensitive), maxDisplayBytes)
}

type keywordMatcher struct {
	keywords      [][]byte
	mode          KeywordMode
	caseSensitive bool
	asciiFold     bool
	maxBytes      int
}

func newKeywordMatcher(keywords []string, mode KeywordMode, caseSensitive bool) *keywordMatcher {
	m := &keywordMatcher{mode: mode, caseSensitive: caseSensitive, asciiFold: !caseSensitive}
	for _, keyword := range keywords {
		if !caseSensitive {
			keyword = strings.ToLower(keyword)
		}
		m.keywords = append(m.keywords, []byte(keyword))
		m.maxBytes = max(m.maxBytes, len(keyword))
		if !isASCII(keyword) {
			m.asciiFold = false
		}
	}
	return m
}

func readPhysicalLineWithMatcher(ctx context.Context, reader *bufio.Reader, matcher *keywordMatcher, maxDisplayBytes int) (physicalLine, error) {
	unlimitedDisplay := maxDisplayBytes < 0
	if maxDisplayBytes == 0 {
		maxDisplayBytes = 1 << 20
	}

	displayCapacity := 64 * 1024
	if !unlimitedDisplay {
		displayCapacity = min(maxDisplayBytes, displayCapacity)
	}
	display := make([]byte, 0, displayCapacity)
	hits := make([]bool, len(matcher.keywords))
	var overlap []byte
	var bytesRead int64

	for {
		if err := ctx.Err(); err != nil {
			return physicalLine{}, err
		}
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			bytesRead += int64(len(fragment))
			remaining := maxDisplayBytes - len(display)
			if unlimitedDisplay {
				display = append(display, fragment...)
			} else if remaining > 0 {
				if remaining > len(fragment) {
					remaining = len(fragment)
				}
				display = append(display, fragment[:remaining]...)
			}

			searchText := fragment
			if len(overlap) > 0 {
				searchText = append(append(make([]byte, 0, len(overlap)+len(fragment)), overlap...), fragment...)
			}
			for i, keyword := range matcher.keywords {
				if !hits[i] && matcher.contains(searchText, keyword) {
					hits[i] = true
				}
			}
			if matcher.maxBytes > 1 {
				keep := matcher.maxBytes - 1
				if keep > len(searchText) {
					keep = len(searchText)
				}
				overlap = append(overlap[:0], searchText[len(searchText)-keep:]...)
			}
		}

		switch {
		case err == nil:
			return finishPhysicalLine(display, bytesRead, maxDisplayBytes, unlimitedDisplay, hits, matcher.mode), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if bytesRead == 0 {
				return physicalLine{}, io.EOF
			}
			return finishPhysicalLine(display, bytesRead, maxDisplayBytes, unlimitedDisplay, hits, matcher.mode), nil
		default:
			return physicalLine{}, err
		}
	}
}

func (m *keywordMatcher) contains(text, keyword []byte) bool {
	if m.caseSensitive {
		return bytes.Contains(text, keyword)
	}
	if m.asciiFold {
		return containsASCIIFold(text, keyword)
	}
	return strings.Contains(strings.ToLower(string(text)), string(keyword))
}

func (m *keywordMatcher) matches(text []byte) bool {
	if m.mode == KeywordAny {
		for _, keyword := range m.keywords {
			if m.contains(text, keyword) {
				return true
			}
		}
		return false
	}
	for _, keyword := range m.keywords {
		if !m.contains(text, keyword) {
			return false
		}
	}
	return true
}

func (m *keywordMatcher) mask(text []byte) uint64 {
	var hits uint64
	for i, keyword := range m.keywords {
		if m.contains(text, keyword) {
			hits |= uint64(1) << i
		}
	}
	return hits
}

func (m *keywordMatcher) maskWithOverlap(fragment []byte, overlap *[]byte) uint64 {
	searchText := fragment
	if len(*overlap) > 0 {
		searchText = append(append(make([]byte, 0, len(*overlap)+len(fragment)), (*overlap)...), fragment...)
	}
	hits := m.mask(searchText)
	*overlap = m.fragmentTail((*overlap)[:0], searchText)
	return hits
}

func (m *keywordMatcher) fragmentTail(dst, text []byte) []byte {
	if m.maxBytes <= 1 {
		return dst[:0]
	}
	keep := min(m.maxBytes-1, len(text))
	return append(dst[:0], text[len(text)-keep:]...)
}

func (m *keywordMatcher) updateHits(hits []bool, overlap, fragment []byte) []byte {
	searchText := fragment
	if len(overlap) > 0 {
		searchText = append(append(make([]byte, 0, len(overlap)+len(fragment)), overlap...), fragment...)
	}
	for i, keyword := range m.keywords {
		if !hits[i] && m.contains(searchText, keyword) {
			hits[i] = true
		}
	}
	if m.maxBytes <= 1 {
		return overlap[:0]
	}
	keep := min(m.maxBytes-1, len(searchText))
	return append(overlap[:0], searchText[len(searchText)-keep:]...)
}

func containsASCIIFold(text, keyword []byte) bool {
	if len(keyword) == 0 {
		return true
	}
	for offset := 0; offset+len(keyword) <= len(text); {
		lastStart := len(text) - len(keyword)
		start := indexFoldedByte(text[offset:lastStart+1], keyword[0])
		if start < 0 {
			return false
		}
		start += offset
		matched := true
		for i, want := range keyword {
			if lowerASCII(text[start+i]) != want {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
		offset = start + 1
	}
	return false
}

func indexFoldedByte(text []byte, lower byte) int {
	upper := lower
	if lower >= 'a' && lower <= 'z' {
		upper = lower - ('a' - 'A')
	}
	lowerIndex := bytes.IndexByte(text, lower)
	if upper == lower {
		return lowerIndex
	}
	upperIndex := bytes.IndexByte(text, upper)
	if lowerIndex < 0 || (upperIndex >= 0 && upperIndex < lowerIndex) {
		return upperIndex
	}
	return lowerIndex
}

func lowerASCII(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] >= 0x80 {
			return false
		}
	}
	return true
}

func finishPhysicalLine(display []byte, bytesRead int64, maxDisplayBytes int, unlimitedDisplay bool, hits []bool, mode KeywordMode) physicalLine {
	display = bytesTrimLineEnding(display)
	truncated := !unlimitedDisplay && bytesRead > int64(maxDisplayBytes)
	text := string(display)
	if truncated {
		text += " ... [line truncated]"
	}

	matched := keywordHitsMatch(hits, mode)
	return physicalLine{text: text, bytes: bytesRead, matched: matched, hits: hits}
}

func keywordHitsMatch(hits []bool, mode KeywordMode) bool {
	matched := mode != KeywordAny
	for _, hit := range hits {
		if mode == KeywordAny && hit {
			matched = true
			break
		}
		if mode != KeywordAny && !hit {
			matched = false
			break
		}
	}
	return matched
}

func bytesTrimLineEnding(value []byte) []byte {
	if len(value) > 0 && value[len(value)-1] == '\n' {
		value = value[:len(value)-1]
	}
	if len(value) > 0 && value[len(value)-1] == '\r' {
		value = value[:len(value)-1]
	}
	return value
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
