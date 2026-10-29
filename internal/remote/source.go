package remote

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var skipDirs = map[string]bool{".git": true, "node_modules": true, "__pycache__": true}

// PackSource builds a deterministic tar.gz of dir and returns it with its content hash.
func PackSource(dir string) ([]byte, string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != dir && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if p != dir {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	sort.Strings(files)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	h := sha256.New()
	for _, p := range files {
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		info, err := os.Lstat(p)
		if err != nil {
			return nil, "", err
		}
		hdr := &tar.Header{Name: rel, Mode: int64(info.Mode().Perm())}
		switch {
		case info.IsDir():
			hdr.Typeflag, hdr.Name = tar.TypeDir, rel+"/"
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return nil, "", err
			}
			hdr.Typeflag, hdr.Linkname = tar.TypeSymlink, link
		case info.Mode().IsRegular():
			hdr.Typeflag, hdr.Size = tar.TypeReg, info.Size()
		default:
			continue
		}
		io.WriteString(h, strings.Join([]string{hdr.Name, hdr.Linkname, info.Mode().Perm().String()}, "\x00"))
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, "", err
		}
		if hdr.Typeflag == tar.TypeReg {
			f, err := os.Open(p)
			if err != nil {
				return nil, "", err
			}
			_, err = io.Copy(io.MultiWriter(tw, h), f)
			f.Close()
			if err != nil {
				return nil, "", err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	if err := gz.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), hex.EncodeToString(h.Sum(nil))[:16], nil
}
