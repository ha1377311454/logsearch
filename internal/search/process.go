package search

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

type ProcessLogRule struct {
	Name         string
	CommRegex    string
	CmdlineRegex string
	IncludeRegex string
	ExcludeRegex string
	LogDirs      []string
	FilePatterns []string
	MaxFiles     int
	MaxFileAge   time.Duration
}

type compiledProcessRule struct {
	rule             ProcessLogRule
	comm, cmdline    *regexp.Regexp
	include, exclude *regexp.Regexp
}

type podIdentity struct {
	Namespace string
	Pod       string
}

var podUIDPattern = regexp.MustCompile(`pod([0-9a-fA-F][0-9a-fA-F_-]{35})`)

func compileProcessRules(rules []ProcessLogRule) ([]compiledProcessRule, error) {
	compiled := make([]compiledProcessRule, 0, len(rules))
	for _, rule := range rules {
		item := compiledProcessRule{rule: rule}
		var err error
		if item.comm, err = optionalRegexp(rule.CommRegex); err != nil {
			return nil, fmt.Errorf("process rule %q comm_regex: %w", rule.Name, err)
		}
		if item.cmdline, err = optionalRegexp(rule.CmdlineRegex); err != nil {
			return nil, fmt.Errorf("process rule %q cmdline_regex: %w", rule.Name, err)
		}
		if item.include, err = optionalRegexp(rule.IncludeRegex); err != nil {
			return nil, fmt.Errorf("process rule %q include_regex: %w", rule.Name, err)
		}
		if item.exclude, err = optionalRegexp(rule.ExcludeRegex); err != nil {
			return nil, fmt.Errorf("process rule %q exclude_regex: %w", rule.Name, err)
		}
		compiled = append(compiled, item)
	}
	return compiled, nil
}

func optionalRegexp(expression string) (*regexp.Regexp, error) {
	if strings.TrimSpace(expression) == "" {
		return nil, nil
	}
	return regexp.Compile(expression)
}

func (s *Service) discoverProcessFiles(ctx context.Context, filter Filter) ([]File, error) {
	if len(s.processRules) == 0 {
		return nil, nil
	}
	pods := s.podIndex()
	processes, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("list host processes: %w", err)
	}
	files := make(map[string]File)
	for _, current := range processes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		comm, err := current.NameWithContext(ctx)
		if err != nil {
			continue
		}
		var cmdline string
		cmdlineLoaded := false
		identity := s.processIdentity(current.Pid, pods)
		// cgroup 路径在不同运行时和 cgroup 版本下格式不同。Pod 元数据成功解析时
		// 执行全局 Pod 约束；解析不到时仍由固定的进程规则和日志路径白名单约束。
		if identity.Pod != "" && !containsAnyFold(identity.Pod, s.opts.PodNameContains) {
			continue
		}
		for _, rule := range s.processRules {
			if rule.comm != nil && !rule.comm.MatchString(comm) {
				continue
			}
			if rule.cmdline != nil {
				if !cmdlineLoaded {
					cmdline, _ = current.CmdlineWithContext(ctx)
					cmdlineLoaded = true
				}
				if !rule.cmdline.MatchString(cmdline) {
					continue
				}
			}
			discovered := s.filesForProcess(ctx, current, identity, rule, filter)
			log.Printf("process log discovery rule=%s pid=%d pod=%s files=%d", rule.rule.Name, current.Pid, identity.Pod, len(discovered))
			for _, file := range discovered {
				files[file.OpenPath] = file
			}
		}
	}
	result := make([]File, 0, len(files))
	for _, file := range files {
		result = append(result, file)
	}
	return result, nil
}

func (s *Service) filesForProcess(ctx context.Context, current *process.Process, identity podIdentity, rule compiledProcessRule, filter Filter) []File {
	var files []File
	seen := make(map[string]struct{})
	add := func(openPath, reportedPath string) {
		if _, ok := seen[openPath]; ok {
			return
		}
		info, err := os.Stat(openPath)
		if err != nil || !info.Mode().IsRegular() || (rule.rule.MaxFileAge > 0 && time.Since(info.ModTime()) > rule.rule.MaxFileAge) {
			return
		}
		file := File{SourceType: "process", Rule: rule.rule.Name, Namespace: identity.Namespace, Pod: identity.Pod, Container: rule.rule.Name, Path: reportedPath, OpenPath: openPath, Size: info.Size(), Modified: info.ModTime()}
		if matchesFilter(file, filter) {
			seen[openPath] = struct{}{}
			files = append(files, file)
		}
	}

	if rule.include != nil {
		openFiles, err := current.OpenFilesWithContext(ctx)
		if err != nil {
			log.Printf("process log open-files failed rule=%s pid=%d error=%v", rule.rule.Name, current.Pid, err)
		} else {
			for _, opened := range openFiles {
				reported := strings.TrimSuffix(opened.Path, " (deleted)")
				if !filepath.IsAbs(reported) || !rule.include.MatchString(reported) || (rule.exclude != nil && rule.exclude.MatchString(reported)) {
					continue
				}
				if source, ok := resolveProcessPath(s.opts.ProcRoot, current.Pid, reported); ok {
					add(source, reported)
				}
			}
		}
	}
	for _, logDir := range rule.rule.LogDirs {
		processDir := filepath.Join(s.opts.ProcRoot, strconv.Itoa(int(current.Pid)), "root", strings.TrimPrefix(filepath.Clean(logDir), string(filepath.Separator)))
		_ = filepath.WalkDir(processDir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				log.Printf("process log directory inaccessible rule=%s pid=%d path=%s error=%v", rule.rule.Name, current.Pid, path, walkErr)
				return nil
			}
			if ctx.Err() != nil {
				return nil
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !matchesPattern(path, rule.rule.FilePatterns) {
				return nil
			}
			relative, err := filepath.Rel(processDir, path)
			if err == nil {
				add(path, filepath.Join(logDir, relative))
			}
			return nil
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Modified.After(files[j].Modified) })
	if rule.rule.MaxFiles > 0 && len(files) > rule.rule.MaxFiles {
		files = files[:rule.rule.MaxFiles]
	}
	return files
}

func resolveProcessPath(procRoot string, pid int32, reportedPath string) (string, bool) {
	path := filepath.Join(procRoot, strconv.Itoa(int(pid)), "root", strings.TrimPrefix(reportedPath, string(filepath.Separator)))
	info, err := os.Stat(path)
	return path, err == nil && info.Mode().IsRegular()
}

func (s *Service) podIndex() map[string]podIdentity {
	result := make(map[string]podIdentity)
	for _, root := range s.roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			parts := strings.SplitN(entry.Name(), "_", 3)
			if len(parts) == 3 {
				result[strings.ToLower(parts[2])] = podIdentity{Namespace: parts[0], Pod: parts[1]}
			}
		}
	}
	return result
}

func (s *Service) processIdentity(pid int32, pods map[string]podIdentity) podIdentity {
	data, err := os.ReadFile(filepath.Join(s.opts.ProcRoot, strconv.Itoa(int(pid)), "cgroup"))
	if err != nil {
		return podIdentity{}
	}
	match := podUIDPattern.FindStringSubmatch(string(data))
	if len(match) != 2 {
		return podIdentity{}
	}
	uid := strings.ToLower(strings.ReplaceAll(match[1], "_", "-"))
	return pods[uid]
}
