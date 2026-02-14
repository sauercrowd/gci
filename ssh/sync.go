package ssh

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func SyncPaths(ctx context.Context, target Target, localBaseDir string, paths []string, excludePatterns []string, remoteDir string) error {
	client, err := Dial(ctx, target)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	reader, writer := io.Pipe()
	session.Stdin = reader

	go func() {
		gzw := gzip.NewWriter(writer)
		tw := tar.NewWriter(gzw)
		writeErr := writeTarArchive(tw, localBaseDir, paths, excludePatterns)
		_ = tw.Close()
		_ = gzw.Close()
		_ = writer.CloseWithError(writeErr)
	}()

	command := fmt.Sprintf("mkdir -p %s && tar -xzf - -C %s", shellQuote(remoteDir), shellQuote(remoteDir))
	if err := session.Run(command); err != nil {
		return fmt.Errorf("failed to sync files to %q: %w", remoteDir, err)
	}

	return nil
}

func writeTarArchive(tw *tar.Writer, baseDir string, paths []string, excludePatterns []string) error {
	added := map[string]struct{}{}

	for _, configuredPath := range paths {
		normalized := filepath.Clean(configuredPath)
		if normalized == "." {
			if err := addDirectoryContentsToTar(tw, baseDir, ".", excludePatterns, added); err != nil {
				return err
			}
			continue
		}

		absPath := filepath.Join(baseDir, filepath.FromSlash(filepath.ToSlash(normalized)))
		info, err := os.Lstat(absPath)
		if err != nil {
			return fmt.Errorf("failed to stat %q: %w", absPath, err)
		}

		if err := addPathToTar(tw, baseDir, normalized, excludePatterns, added); err != nil {
			return err
		}
		if info.IsDir() {
			if err := addDirectoryContentsToTar(tw, baseDir, normalized, excludePatterns, added); err != nil {
				return err
			}
		}
	}

	return nil
}

func addDirectoryContentsToTar(tw *tar.Writer, baseDir, relativeDir string, excludePatterns []string, added map[string]struct{}) error {
	absDir := filepath.Join(baseDir, relativeDir)
	return filepath.WalkDir(absDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relative, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			return nil
		}

		if isExcluded(relative, excludePatterns) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		return addPathToTar(tw, baseDir, relative, excludePatterns, added)
	})
}

func addPathToTar(tw *tar.Writer, baseDir, relativePath string, excludePatterns []string, added map[string]struct{}) error {
	relativePath = filepath.ToSlash(filepath.Clean(relativePath))
	if isExcluded(relativePath, excludePatterns) {
		return nil
	}
	if _, exists := added[relativePath]; exists {
		return nil
	}

	absPath := filepath.Join(baseDir, filepath.FromSlash(relativePath))
	info, err := os.Lstat(absPath)
	if err != nil {
		return fmt.Errorf("failed to stat %q: %w", absPath, err)
	}

	linkTarget := ""
	if info.Mode()&os.ModeSymlink != 0 {
		linkTarget, err = os.Readlink(absPath)
		if err != nil {
			return fmt.Errorf("failed to read symlink %q: %w", absPath, err)
		}
	}

	header, err := tar.FileInfoHeader(info, linkTarget)
	if err != nil {
		return fmt.Errorf("failed to build tar header for %q: %w", absPath, err)
	}
	header.Name = relativePath
	if info.IsDir() && !strings.HasSuffix(header.Name, "/") {
		header.Name += "/"
	}

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to write tar header for %q: %w", absPath, err)
	}
	added[relativePath] = struct{}{}

	if info.IsDir() {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}

	file, err := os.Open(absPath)
	if err != nil {
		return fmt.Errorf("failed to open %q: %w", absPath, err)
	}
	defer file.Close()

	if _, err := io.Copy(tw, file); err != nil {
		return fmt.Errorf("failed to copy %q into tar stream: %w", absPath, err)
	}

	return nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func isExcluded(relativePath string, patterns []string) bool {
	relativePath = filepath.ToSlash(strings.TrimPrefix(relativePath, "./"))
	for _, pattern := range patterns {
		p := filepath.ToSlash(strings.TrimSpace(pattern))
		if p == "" {
			continue
		}

		if matchExcludePattern(relativePath, p) {
			return true
		}
	}

	return false
}

func matchExcludePattern(relativePath, pattern string) bool {
	if strings.HasSuffix(pattern, "/") {
		dir := strings.TrimSuffix(pattern, "/")
		return relativePath == dir || strings.HasPrefix(relativePath, dir+"/")
	}

	if ok, _ := path.Match(pattern, relativePath); ok {
		return true
	}

	base := path.Base(relativePath)
	if !strings.Contains(pattern, "/") {
		if ok, _ := path.Match(pattern, base); ok {
			return true
		}
		if pathHasSegment(relativePath, pattern) {
			return true
		}
	}

	return false
}

func pathHasSegment(relativePath, segment string) bool {
	parts := strings.Split(relativePath, "/")
	for _, part := range parts {
		if part == segment {
			return true
		}
	}
	return false
}
