package collector

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// rotatedPaths returns the base log path plus all readable rotated variants,
// sorted chronologically oldest-first. Supports:
//   - Numbered:    auth.log.2.gz, auth.log.1, auth.log
//   - Date-based:  dnf.rpm.log-20260315.gz, dnf.rpm.log-20260401, dnf.rpm.log
func rotatedPaths(base string) []string {
	dir := filepath.Dir(base)
	name := filepath.Base(base)

	var candidates []string

	// Numbered variants (up to 8 rotations, oldest first)
	for i := 8; i >= 1; i-- {
		candidates = append(candidates,
			filepath.Join(dir, fmt.Sprintf("%s.%d.gz", name, i)),
			filepath.Join(dir, fmt.Sprintf("%s.%d", name, i)),
		)
	}

	// Date-based variants: match "<name>-YYYYMMDD" and "<name>-YYYYMMDD.gz"
	dateMatches, _ := filepath.Glob(filepath.Join(dir, name+"-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]*"))
	sort.Strings(dateMatches) // lexicographic order = chronological for YYYYMMDD prefix
	candidates = append(candidates, dateMatches...)

	// Active log always last (most recent)
	candidates = append(candidates, base)

	// Filter to only files that exist and are readable
	var result []string
	seen := make(map[string]bool)
	for _, p := range candidates {
		if seen[p] {
			continue
		}
		seen[p] = true
		if _, err := os.Stat(p); err == nil {
			result = append(result, p)
		}
	}
	return result
}

// multiFileReader concatenates all paths into a single io.Reader, decompressing
// .gz files transparently. Unreadable files are silently skipped.
// The caller must close all returned closers when done.
func multiFileReader(paths []string) (io.Reader, []io.Closer, error) {
	var readers []io.Reader
	var closers []io.Closer

	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue // skip unreadable files (permissions, missing)
		}
		closers = append(closers, f)

		if strings.HasSuffix(p, ".gz") {
			gz, err := gzip.NewReader(f)
			if err != nil {
				// Corrupt or empty gzip — skip the gz reader but keep file open for cleanup
				continue
			}
			closers = append(closers, gz)
			readers = append(readers, gz)
		} else {
			readers = append(readers, f)
		}
	}

	if len(readers) == 0 {
		return strings.NewReader(""), closers, nil
	}
	return io.MultiReader(readers...), closers, nil
}
