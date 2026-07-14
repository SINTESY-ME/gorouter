// Filters for RTK compression. Each filter takes raw tool output text and
// returns a compressed but semantically-equivalent string. All filters must
// be safe to call on any input (never panic, never return empty when input
// is non-empty). The autodetect function picks one based on the first 1KB.
//
// Regex patterns are compiled at package init (regexp.MustCompile), matching
// the Rust lazy_static! convention — never compile inside a filter function.

package rtk

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Truncation caps (mirrors rtk/src/core/truncate.rs).
const (
	capErrors    = 20
	capWarnings  = 10
	capList      = 20
	capInventory = 50
)

// Filter-specific caps (mirrors rtk constants.js).
const (
	gitDiffHunkMaxLines  = 100
	gitLogMaxLines       = 200
	dedupLineMax         = 2000
	grepPerFileMax       = 10
	findPerDirMax        = 10
	findTotalDirMax      = 20
	statusMaxFiles       = 10
	statusMaxUntracked   = 10
	lsExtSummaryTop      = 5
	treeMaxLines         = 200
	smartTruncateHead    = 120
	smartTruncateTail    = 60
	smartTruncateMin     = 250
	readNumberedMinRatio = 0.7
)

// noiseDirs are skipped by the ls filter (mirrors LS_NOISE_DIRS).
var noiseDirs = map[string]bool{
	"node_modules": true, ".git": true, "target": true, "__pycache__": true,
	".next": true, "dist": true, "build": true, ".cache": true, ".turbo": true,
	".vercel": true, ".pytest_cache": true, ".mypy_cache": true, ".tox": true,
	".venv": true, "venv": true, "env": true, "coverage": true,
	".nyc_output": true, ".DS_Store": true, "Thumbs.db": true,
	".idea": true, ".vscode": true, ".vs": true, ".egg-info": true, ".eggs": true,
}

// rtkFilterFunc is a filter function with a name attached.
type rtkFilterFunc struct {
	name   string
	filter func(string) string
}

func (f *rtkFilterFunc) Name() string { return f.name }

// Package-level regexes (compiled once, like lazy_static!).
var (
	reGitDiff      = regexp.MustCompile(`(?m)^diff --git `)
	reGitDiffHunk  = regexp.MustCompile(`(?m)^@@ `)
	reGitStatus    = regexp.MustCompile(`(?m)^On branch |^nothing to commit|^Changes (not |to be )|^Untracked files:`)
	reGitLog       = regexp.MustCompile(`(?m)^[*|/\\ ]*commit [0-9a-f]{7,40}$`)
	rePorcelain    = regexp.MustCompile(`(?m)^[ MADRCU?!][ MADRCU?!] \S`)
	reBuildOutput  = regexp.MustCompile(`(?im)^(npm (warn|error|ERR!)|yarn (warn|error)|\s*Compiling\s+\S+|\s*Downloading\s+\S+|added \d+ package|\[ERROR\]|BUILD (SUCCESS|FAILED)|\s*Finished\s+|Successfully (installed|built)|ERROR:)`)
	reTreeGlyph    = regexp.MustCompile(`[├└]──|│  `)
	reLSRow        = regexp.MustCompile(`(?m)^[-dlbcps][rwx-]{9}`)
	reLSTotal      = regexp.MustCompile(`(?m)^total \d+$`)
	reLSDate       = regexp.MustCompile(`\s+(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+(\d{4}|\d{2}:\d{2})\s+`)
	reReadNumbered  = regexp.MustCompile(`^\s*\d+\|`)
	reSearchList    = regexp.MustCompile(`^Result of search in '[^']*' \(total (\d+) files?\):`)
	reGitLogCommit  = regexp.MustCompile(`(?i)^commit [0-9a-f]{7,40}$`)
	reGitLogGraph   = regexp.MustCompile(`(?i)^[*|/\\ ]+commit [0-9a-f]{7,40}`)
	reGitLogAuthor  = regexp.MustCompile(`(?i)^[*|/\\ ]*(Author|Date):`)
	reGitLogSubject = regexp.MustCompile(`^[*|/\\ ]*    \S`)
	reGitLogStat    = regexp.MustCompile(`^\d+ file\w* changed`)
	reGitLogDiff    = regexp.MustCompile(`^diff --git `)
	reGitLogGraphSHA = regexp.MustCompile(`(?i)^[*|/\\ ]+([0-9a-f]{7,40}\s+.+)`)
	reGitLogOneline = regexp.MustCompile(`^[0-9a-f]{7,40}\s+`)
	reGitLogGraphOnly = regexp.MustCompile(`^[*|/\\ ]+$`)
	reLongBranch   = regexp.MustCompile(`^On branch (\S+)`)
	reLongForm     = regexp.MustCompile(`^\s*(modified|new file|deleted|renamed|both modified):\s+(.+)$`)
	reCargoErrCont = regexp.MustCompile(`^\s*(-->|\||\d+\s*\||=)`)
)

// autoDetectFilter picks the best filter by scanning the first 1KB.
// Priority order mirrors rtk/src/cmds/system/pipe_cmd.rs::auto_detect_filter.
func autoDetectFilter(text string) *rtkFilterFunc {
	end := len(text)
	if end > detectWindow {
		end = floorCharBoundary(text, detectWindow)
	}
	head := text[:end]

	if reGitLog.MatchString(head) {
		return &rtkFilterFunc{"git-log", filterGitLog}
	}
	if reGitDiff.MatchString(head) || reGitDiffHunk.MatchString(head) {
		return &rtkFilterFunc{"git-diff", filterGitDiff}
	}
	if reGitStatus.MatchString(head) {
		return &rtkFilterFunc{"git-status", filterGitStatus}
	}
	if reBuildOutput.MatchString(head) {
		return &rtkFilterFunc{"build-output", filterBuildOutput}
	}
	if isMostlyPorcelain(head) {
		return &rtkFilterFunc{"git-status", filterGitStatus}
	}

	lines := strings.Split(head, "\n")
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}

	// grep: first 5 non-empty lines, any match "file:number:content"
	if len(nonEmpty) >= 1 {
		for i := 0; i < 5 && i < len(nonEmpty); i++ {
			if isGrepLine(nonEmpty[i]) {
				return &rtkFilterFunc{"grep", filterGrep}
			}
		}
	}

	// find: ALL non-empty lines path-like, >=3 lines
	if len(nonEmpty) >= 3 && allPathLike(nonEmpty) {
		return &rtkFilterFunc{"find", filterFind}
	}

	// tree: box-drawing glyphs
	if reTreeGlyph.MatchString(head) {
		return &rtkFilterFunc{"tree", filterTree}
	}

	// ls: "total N" header or >=3 perm-string rows
	if reLSTotal.MatchString(head) || countMatches(head, reLSRow) >= 3 {
		return &rtkFilterFunc{"ls", filterLS}
	}

	// Cursor Glob search list header
	if reSearchList.MatchString(head) {
		return &rtkFilterFunc{"search-list", filterSearchList}
	}

	// read-numbered: "N|content" lines
	if len(lines) >= smartTruncateMin && isLineNumbered(lines) {
		return &rtkFilterFunc{"read-numbered", filterReadNumbered}
	}

	// dedup-log: generic multi-line noise with duplicates, >=5 non-empty lines
	if len(nonEmpty) >= 5 {
		return &rtkFilterFunc{"dedup-log", filterDedupLog}
	}

	// smart-truncate: big blob with no structure
	if strings.Count(text, "\n") >= smartTruncateMin {
		return &rtkFilterFunc{"smart-truncate", filterSmartTruncate}
	}

	return nil
}

// safeApply runs a filter with panic recovery (fail-open).
func safeApply(fn *rtkFilterFunc, text string) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = text
		}
	}()
	return fn.filter(text)
}

// --- Filters ---

// filterGitDiff compacts unified diffs: file headers, hunk truncation at 100
// lines, +N -M totals.
func filterGitDiff(diff string) string {
	return filterGitDiffWithMax(diff, 500)
}

func filterGitDiffWithMax(diff string, maxLines int) string {
	var result []string
	currentFile := ""
	added, removed := 0, 0
	inHunk := false
	hunkShown, hunkSkipped := 0, 0
	wasTruncated := false
	maxHunkLines := gitDiffHunkMaxLines

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git"):
			if hunkSkipped > 0 {
				result = append(result, "  ... ("+strconv.Itoa(hunkSkipped)+" lines truncated)")
				wasTruncated = true
				hunkSkipped = 0
			}
			if currentFile != "" && (added > 0 || removed > 0) {
				result = append(result, "  +"+strconv.Itoa(added)+" -"+strconv.Itoa(removed))
			}
			parts := strings.SplitN(line, " b/", 2)
			if len(parts) > 1 {
				currentFile = parts[1]
			} else {
				currentFile = "unknown"
			}
			result = append(result, "\n"+currentFile)
			added, removed = 0, 0
			inHunk = false
			hunkShown = 0
		case strings.HasPrefix(line, "@@"):
			if hunkSkipped > 0 {
				result = append(result, "  ... ("+strconv.Itoa(hunkSkipped)+" lines truncated)")
				wasTruncated = true
				hunkSkipped = 0
			}
			inHunk = true
			hunkShown = 0
			result = append(result, "  "+line)
		case inHunk:
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				added++
				if hunkShown < maxHunkLines {
					result = append(result, "  "+line)
					hunkShown++
				} else {
					hunkSkipped++
				}
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				removed++
				if hunkShown < maxHunkLines {
					result = append(result, "  "+line)
					hunkShown++
				} else {
					hunkSkipped++
				}
			} else if hunkShown < maxHunkLines && !strings.HasPrefix(line, "\\") {
				if hunkShown > 0 {
					result = append(result, "  "+line)
					hunkShown++
				}
			}
		}
		if len(result) >= maxLines {
			result = append(result, "\n... (more changes truncated)")
			wasTruncated = true
			goto done
		}
	}
done:
	if hunkSkipped > 0 {
		result = append(result, "  ... ("+strconv.Itoa(hunkSkipped)+" lines truncated)")
		wasTruncated = true
	}
	if currentFile != "" && (added > 0 || removed > 0) {
		result = append(result, "  +"+strconv.Itoa(added)+" -"+strconv.Itoa(removed))
	}
	if wasTruncated {
		result = append(result, "[full diff: rtk git diff --no-compact]")
	}
	return strings.Join(result, "\n")
}

// filterGitLog compresses git log output.
func filterGitLog(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	var out []string
	skipped := 0
	inCommit := false
	subjectSeen := false

	push := func(l string) {
		if len(out) < gitLogMaxLines {
			out = append(out, l)
		} else {
			skipped++
		}
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)

		if reGitLogCommit.MatchString(trimmed) || reGitLogGraph.MatchString(trimmed) {
			inCommit = true
			subjectSeen = false
			push(line)
			continue
		}
		if inCommit {
			if reGitLogAuthor.MatchString(trimmed) {
				push(trimmed)
				continue
			}
			if trimmed == "" {
				continue
			}
			if !subjectSeen && reGitLogSubject.MatchString(line) {
				push("  Subject: " + trimmed)
				subjectSeen = true
				continue
			}
			if reGitLogStat.MatchString(trimmed) {
				push("  " + trimmed)
				continue
			}
			if reGitLogDiff.MatchString(trimmed) {
				push("  ... diff body omitted")
				continue
			}
			continue
		}
		// Not in a commit block (--oneline / --graph)
		if m := reGitLogGraphSHA.FindStringSubmatch(trimmed); m != nil {
			push(m[1])
			continue
		}
		if reGitLogOneline.MatchString(trimmed) {
			push(trimmed)
			continue
		}
		if reGitLogGraphOnly.MatchString(trimmed) && strings.ContainsAny(trimmed, "*|/\\") {
			continue
		}
		push(trimmed)
	}
	if skipped > 0 {
		out = append(out, "... ("+strconv.Itoa(skipped)+" more lines)")
	}
	result := strings.Join(out, "\n")
	if result == "" && text != "" {
		return text
	}
	if len(result) > len(text) {
		return text
	}
	return result
}

// filterGitStatus compacts git status output.
func filterGitStatus(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 || (len(lines) == 1 && strings.TrimSpace(lines[0]) == "") {
		return "Clean working tree"
	}
	branch := ""
	var stagedFiles, modifiedFiles, untrackedFiles []string
	staged, modified, untracked, conflicts := 0, 0, 0, 0

	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if m := reLongBranch.FindStringSubmatch(raw); m != nil {
			branch = m[1]
			continue
		}
		if strings.HasPrefix(raw, "##") {
			branch = strings.TrimPrefix(raw, "## ")
			// strip leading whitespace
			branch = strings.TrimSpace(branch)
			continue
		}
		if len(raw) >= 3 && rePorcelain.MatchString(raw) {
			x := raw[0]
			y := raw[1]
			file := raw[3:]
			if raw[:2] == "??" {
				untracked++
				untrackedFiles = append(untrackedFiles, file)
				continue
			}
			if strings.ContainsRune("MADRC", rune(x)) {
				staged++
				stagedFiles = append(stagedFiles, file)
			} else if x == 'U' {
				conflicts++
			}
			if y == 'M' || y == 'D' {
				modified++
				modifiedFiles = append(modifiedFiles, file)
			}
			continue
		}
		if m := reLongForm.FindStringSubmatch(raw); m != nil {
			kind := m[1]
			path := strings.TrimSpace(m[2])
			switch kind {
			case "both modified":
				conflicts++
			case "modified", "deleted":
				modified++
				modifiedFiles = append(modifiedFiles, path)
			case "new file", "renamed":
				staged++
				stagedFiles = append(stagedFiles, path)
			}
		}
	}

	var out strings.Builder
	if branch != "" {
		out.WriteString("* " + branch + "\n")
	}
	if staged > 0 {
		out.WriteString("+ Staged: " + strconv.Itoa(staged) + " files\n")
		for i, f := range stagedFiles {
			if i >= statusMaxFiles {
				out.WriteString("   ... +" + strconv.Itoa(len(stagedFiles)-statusMaxFiles) + " more\n")
				break
			}
			out.WriteString("   " + f + "\n")
		}
	}
	if modified > 0 {
		out.WriteString("~ Modified: " + strconv.Itoa(modified) + " files\n")
		for i, f := range modifiedFiles {
			if i >= statusMaxFiles {
				out.WriteString("   ... +" + strconv.Itoa(len(modifiedFiles)-statusMaxFiles) + " more\n")
				break
			}
			out.WriteString("   " + f + "\n")
		}
	}
	if untracked > 0 {
		out.WriteString("? Untracked: " + strconv.Itoa(untracked) + " files\n")
		for i, f := range untrackedFiles {
			if i >= statusMaxUntracked {
				out.WriteString("   ... +" + strconv.Itoa(len(untrackedFiles)-statusMaxUntracked) + " more\n")
				break
			}
			out.WriteString("   " + f + "\n")
		}
	}
	if conflicts > 0 {
		out.WriteString("conflicts: " + strconv.Itoa(conflicts) + " files\n")
	}
	if staged == 0 && modified == 0 && untracked == 0 && conflicts == 0 {
		out.WriteString("clean — nothing to commit\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

// filterGrep groups grep output by file, caps matches per file.
func filterGrep(input string) string {
	byFile := map[string][][2]string{}
	var files []string
	total := 0
	for _, line := range strings.Split(input, "\n") {
		first := strings.Index(line, ":")
		if first == -1 {
			continue
		}
		second := strings.Index(line[first+1:], ":")
		if second == -1 {
			continue
		}
		second += first + 1
		file := line[:first]
		lineNum := line[first+1 : second]
		content := line[second+1:]
		if _, err := strconv.Atoi(lineNum); err != nil {
			continue
		}
		total++
		if _, ok := byFile[file]; !ok {
			files = append(files, file)
		}
		byFile[file] = append(byFile[file], [2]string{lineNum, content})
	}
	if total == 0 {
		return input
	}
	sort.Strings(files)
	var out strings.Builder
	out.WriteString(strconv.Itoa(total) + " matches in " + strconv.Itoa(len(files)) + "F:\n\n")
	for _, file := range files {
		matches := byFile[file]
		out.WriteString("[file] " + file + " (" + strconv.Itoa(len(matches)) + "):\n")
		for i := 0; i < len(matches) && i < grepPerFileMax; i++ {
			out.WriteString("  " + padLeft(matches[i][0], 4) + ": " + strings.TrimSpace(matches[i][1]) + "\n")
		}
		if len(matches) > grepPerFileMax {
			out.WriteString("  +" + strconv.Itoa(len(matches)-grepPerFileMax) + "\n")
		}
		out.WriteString("\n")
	}
	return out.String()
}

// filterFind groups find output by parent dir.
func filterFind(input string) string {
	var lines []string
	for _, l := range strings.Split(input, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return input
	}
	byDir := map[string][]string{}
	var dirs []string
	for _, path := range lines {
		lastSlash := strings.LastIndex(path, "/")
		var dir, basename string
		if lastSlash == -1 {
			dir = "."
			basename = path
		} else {
			dir = path[:lastSlash]
			if dir == "" {
				dir = "/"
			}
			basename = path[lastSlash+1:]
		}
		if _, ok := byDir[dir]; !ok {
			dirs = append(dirs, dir)
		}
		byDir[dir] = append(byDir[dir], basename)
	}
	sort.Strings(dirs)
	var out strings.Builder
	out.WriteString(strconv.Itoa(len(lines)) + " files in " + strconv.Itoa(len(dirs)) + " dirs:\n\n")
	for i, dir := range dirs {
		if i >= findTotalDirMax {
			break
		}
		names := byDir[dir]
		out.WriteString(dir + "/  (" + strconv.Itoa(len(names)) + ")\n")
		for j := 0; j < len(names) && j < findPerDirMax; j++ {
			out.WriteString("  " + names[j] + "\n")
		}
		if len(names) > findPerDirMax {
			out.WriteString("  +" + strconv.Itoa(len(names)-findPerDirMax) + "\n")
		}
	}
	if len(dirs) > findTotalDirMax {
		out.WriteString("\n+" + strconv.Itoa(len(dirs)-findTotalDirMax) + " more dirs\n")
	}
	return out.String()
}

// filterLS compacts ls -la output.
func filterLS(input string) string {
	var dirs, files [][2]string // [name, size]
	byExt := map[string]int{}
	for _, line := range strings.Split(input, "\n") {
		if strings.HasPrefix(line, "total ") || line == "" {
			continue
		}
		parsed := parseLSLine(line)
		if parsed == nil {
			continue
		}
		if parsed.name == "." || parsed.name == ".." || noiseDirs[parsed.name] {
			continue
		}
		if parsed.fileType == "d" {
			dirs = append(dirs, [2]string{parsed.name, ""})
		} else if parsed.fileType == "-" || parsed.fileType == "l" {
			dot := strings.LastIndex(parsed.name, ".")
			var ext string
			if dot > 0 {
				ext = parsed.name[dot:]
			} else {
				ext = "no ext"
			}
			byExt[ext]++
			files = append(files, [2]string{parsed.name, humanSize(parsed.size)})
		}
	}
	if len(dirs) == 0 && len(files) == 0 {
		return input
	}
	var out strings.Builder
	for _, d := range dirs {
		out.WriteString(d[0] + "/\n")
	}
	for _, f := range files {
		out.WriteString(f[0] + "  " + f[1] + "\n")
	}
	out.WriteString("\nSummary: " + strconv.Itoa(len(files)) + " files, " + strconv.Itoa(len(dirs)) + " dirs")
	if len(byExt) > 0 {
		var exts []string
		for e := range byExt {
			exts = append(exts, e)
		}
		sort.Slice(exts, func(i, j int) bool { return byExt[exts[i]] > byExt[exts[j]] })
		out.WriteString(" (")
		for i, e := range exts {
			if i >= lsExtSummaryTop {
				break
			}
			if i > 0 {
				out.WriteString(", ")
			}
			out.WriteString(strconv.Itoa(byExt[e]) + " " + e)
		}
		if len(exts) > lsExtSummaryTop {
			out.WriteString(", +" + strconv.Itoa(len(exts)-lsExtSummaryTop) + " more")
		}
		out.WriteString(")")
	}
	return out.String()
}

type lsLine struct {
	fileType string
	size     int64
	name     string
}

func parseLSLine(line string) *lsLine {
	loc := reLSDate.FindStringIndex(line)
	if loc == nil {
		return nil
	}
	name := line[loc[1]:]
	before := line[:loc[0]]
	beforeParts := strings.Fields(before)
	if len(beforeParts) < 4 {
		return nil
	}
	perms := beforeParts[0]
	var size int64
	for i := len(beforeParts) - 1; i >= 0; i-- {
		if n, err := strconv.ParseInt(beforeParts[i], 10, 64); err == nil && strconv.FormatInt(n, 10) == beforeParts[i] {
			size = n
			break
		}
	}
	return &lsLine{fileType: string(perms[0]), size: size, name: name}
}

func humanSize(bytes int64) string {
	if bytes >= 1048576 {
		return strconv.FormatFloat(float64(bytes)/1048576, 'f', 1, 64) + "M"
	}
	if bytes >= 1024 {
		return strconv.FormatFloat(float64(bytes)/1024, 'f', 1, 64) + "K"
	}
	return strconv.FormatInt(bytes, 10) + "B"
}

// filterTree strips summary and caps.
func filterTree(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 {
		return input
	}
	var filtered []string
	for _, line := range lines {
		if strings.Contains(line, "director") && strings.Contains(line, "file") {
			continue
		}
		if strings.TrimSpace(line) == "" && len(filtered) == 0 {
			continue
		}
		filtered = append(filtered, line)
	}
	for len(filtered) > 0 && strings.TrimSpace(filtered[len(filtered)-1]) == "" {
		filtered = filtered[:len(filtered)-1]
	}
	if len(filtered) > treeMaxLines {
		cut := len(filtered) - treeMaxLines
		return strings.Join(filtered[:treeMaxLines], "\n") + "\n... +" + strconv.Itoa(cut) + " more lines"
	}
	return strings.Join(filtered, "\n")
}

// filterBuildOutput strips progress, keeps errors/warnings/summary.
func filterBuildOutput(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 {
		return input
	}
	var errors, warnings, deprecations []string
	var summary string
	compilingCount, downloadingCount := 0, 0
	inCargoError := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inCargoError {
			if trimmed == "" {
				inCargoError = false
				continue
			}
			if reCargoErrCont.MatchString(line) {
				errors = append(errors, line)
				continue
			}
			inCargoError = false
		}
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		switch {
		case strings.HasPrefix(lower, "npm err!") || strings.HasPrefix(lower, "npm error") || strings.HasPrefix(lower, "yarn error"):
			errors = append(errors, line)
		case strings.HasPrefix(lower, "npm warn deprecated"):
			deprecations = append(deprecations, line)
		case strings.HasPrefix(lower, "npm warn") || strings.HasPrefix(lower, "yarn warn"):
			warnings = append(warnings, line)
		case strings.HasPrefix(lower, "error[") || strings.HasPrefix(lower, "error:") || strings.HasPrefix(trimmed, "error -->"):
			errors = append(errors, line)
			inCargoError = true
		case strings.HasPrefix(lower, "warning[") || strings.HasPrefix(lower, "warning:") || strings.HasPrefix(trimmed, "warning -->"):
			warnings = append(warnings, line)
			inCargoError = true
		case strings.HasPrefix(lower, "error:"):
			errors = append(errors, line)
		case strings.HasPrefix(trimmed, "[ERROR]") || strings.HasPrefix(lower, "build failed"):
			errors = append(errors, line)
		case strings.HasPrefix(trimmed, "[WARNING]"):
			warnings = append(warnings, line)
		case strings.HasPrefix(trimmed, "Compiling ") || strings.HasPrefix(trimmed, "    Compiling "):
			compilingCount++
		case strings.HasPrefix(trimmed, "Downloading ") || strings.HasPrefix(trimmed, "Fetching "):
			downloadingCount++
		case isBuildSummary(trimmed):
			if summary != "" {
				summary += "\n" + line
			} else {
				summary = line
			}
		}
	}
	var out strings.Builder
	keepDep := 3
	for i := 0; i < len(deprecations) && i < keepDep; i++ {
		out.WriteString(deprecations[i] + "\n")
	}
	if len(deprecations) > keepDep {
		out.WriteString("... +" + strconv.Itoa(len(deprecations)-keepDep) + " more deprecated packages\n")
	}
	if compilingCount > 0 {
		out.WriteString("Compiled " + strconv.Itoa(compilingCount) + " packages\n")
	}
	if downloadingCount > 0 {
		out.WriteString("Downloaded " + strconv.Itoa(downloadingCount) + " packages\n")
	}
	for _, e := range errors {
		out.WriteString(e + "\n")
	}
	keepWarn := 5
	for i := 0; i < len(warnings) && i < keepWarn; i++ {
		out.WriteString(warnings[i] + "\n")
	}
	if len(warnings) > keepWarn {
		out.WriteString("... +" + strconv.Itoa(len(warnings)-keepWarn) + " more warnings\n")
	}
	if summary != "" {
		out.WriteString(summary + "\n")
	}
	result := strings.TrimRight(out.String(), "\n")
	if result == "" {
		return input
	}
	return result
}

func isBuildSummary(trimmed string) bool {
	lower := strings.ToLower(trimmed)
	switch {
	case regexp.MustCompile(`^(added|removed|changed|audited|installed)\s+\d+\s+package`).MatchString(lower):
		return true
	case strings.HasPrefix(lower, "finished "):
		return true
	case strings.HasPrefix(lower, "build success"):
		return true
	case regexp.MustCompile(`^\d+\s+(vulnerabilities|packages?|warnings?|errors?)`).MatchString(lower):
		return true
	case strings.HasPrefix(lower, "successfully installed") || strings.HasPrefix(lower, "successfully built"):
		return true
	case strings.HasPrefix(lower, "to address "):
		return true
	case strings.HasPrefix(lower, "run `npm audit") || strings.HasPrefix(lower, "run `npm fund"):
		return true
	case strings.Contains(lower, "packages are looking for funding"):
		return true
	}
	return false
}

// filterDedupLog collapses consecutive duplicate lines, caps at 2000.
func filterDedupLog(input string) string {
	lines := strings.Split(input, "\n")
	var out []string
	var prev *string
	runCount := 0
	blankStreak := 0
	flushRun := func() {
		if prev != nil && runCount > 1 {
			out = append(out, "  ... ("+strconv.Itoa(runCount-1)+" duplicate lines)")
		}
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blankStreak < 1 {
				out = append(out, line)
			}
			blankStreak++
			flushRun()
			prev = nil
			runCount = 0
			continue
		}
		blankStreak = 0
		if prev != nil && line == *prev {
			runCount++
			continue
		}
		flushRun()
		out = append(out, line)
		l := line
		prev = &l
		runCount = 1
		if len(out) >= dedupLineMax {
			out = append(out, "... (truncated at "+strconv.Itoa(dedupLineMax)+" lines)")
			return strings.Join(out, "\n")
		}
	}
	flushRun()
	return strings.Join(out, "\n")
}

// filterReadNumbered keeps head+tail of "N|content" numbered file dumps.
func filterReadNumbered(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) < smartTruncateMin {
		return input
	}
	head := lines[:smartTruncateHead]
	tail := lines[len(lines)-smartTruncateTail:]
	cut := len(lines) - len(head) - len(tail)
	result := make([]string, 0, len(head)+1+len(tail))
	result = append(result, head...)
	result = append(result, "... +"+strconv.Itoa(cut)+" lines truncated (file continues)")
	result = append(result, tail...)
	return strings.Join(result, "\n")
}

// filterSearchList compacts Cursor Glob search list output.
func filterSearchList(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 {
		return input
	}
	header := lines[0]
	if !reSearchList.MatchString(header) {
		return input
	}
	rest := lines[1:]
	var paths []string
	for _, raw := range rest {
		t := strings.TrimSpace(raw)
		if !strings.HasPrefix(t, "- ") {
			continue
		}
		paths = append(paths, t[2:])
	}
	if len(paths) == 0 {
		return input
	}
	byDir := map[string][]string{}
	var dirs []string
	for _, p := range paths {
		slash := strings.LastIndex(p, "/")
		var dir, name string
		if slash == -1 {
			dir = "."
			name = p
		} else {
			dir = p[:slash]
			if dir == "" {
				dir = "/"
			}
			name = p[slash+1:]
		}
		if _, ok := byDir[dir]; !ok {
			dirs = append(dirs, dir)
		}
		byDir[dir] = append(byDir[dir], name)
	}
	sort.Strings(dirs)
	var out strings.Builder
	out.WriteString(header + "\n" + strconv.Itoa(len(paths)) + " files in " + strconv.Itoa(len(dirs)) + " dirs:\n\n")
	for i, dir := range dirs {
		if i >= findTotalDirMax {
			break
		}
		names := byDir[dir]
		out.WriteString(dir + "/ (" + strconv.Itoa(len(names)) + "):\n")
		for j := 0; j < len(names) && j < findPerDirMax; j++ {
			out.WriteString("  " + names[j] + "\n")
		}
		if len(names) > findPerDirMax {
			out.WriteString("  +" + strconv.Itoa(len(names)-findPerDirMax) + "\n")
		}
		out.WriteString("\n")
	}
	if len(dirs) > findTotalDirMax {
		out.WriteString("+" + strconv.Itoa(len(dirs)-findTotalDirMax) + " more dirs\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

// filterSmartTruncate keeps head+tail, replaces middle with marker.
func filterSmartTruncate(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) < smartTruncateMin {
		return input
	}
	head := lines[:smartTruncateHead]
	tail := lines[len(lines)-smartTruncateTail:]
	cut := len(lines) - len(head) - len(tail)
	result := make([]string, 0, len(head)+1+len(tail))
	result = append(result, head...)
	result = append(result, "... +"+strconv.Itoa(cut)+" lines truncated")
	result = append(result, tail...)
	return strings.Join(result, "\n")
}

// --- Helpers ---

func isGrepLine(line string) bool {
	first := strings.Index(line, ":")
	if first == -1 {
		return false
	}
	second := strings.Index(line[first+1:], ":")
	if second == -1 {
		return false
	}
	second += first + 1
	_, err := strconv.Atoi(line[first+1 : second])
	return err == nil
}

func isPathLike(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" || strings.Contains(t, ":") {
		return false
	}
	return strings.HasPrefix(t, ".") || strings.HasPrefix(t, "/") || strings.Contains(t, "/")
}

func allPathLike(lines []string) bool {
	for _, l := range lines {
		if !isPathLike(l) {
			return false
		}
	}
	return true
}

func isMostlyPorcelain(head string) bool {
	var lines []string
	for _, l := range strings.Split(head, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) < 3 {
		return false
	}
	hits := 0
	for _, l := range lines {
		if rePorcelain.MatchString(l) {
			hits++
		}
	}
	return float64(hits)/float64(len(lines)) >= 0.6
}

func isLineNumbered(lines []string) bool {
	hits := 0
	nonEmpty := 0
	sample := lines
	if len(sample) > 100 {
		sample = sample[:100]
	}
	for _, l := range sample {
		if l == "" {
			continue
		}
		nonEmpty++
		if reReadNumbered.MatchString(l) {
			hits++
		}
	}
	if nonEmpty < 5 {
		return false
	}
	return float64(hits)/float64(nonEmpty) >= readNumberedMinRatio
}

func countMatches(text string, re *regexp.Regexp) int {
	return len(re.FindAllString(text, -1))
}

func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}