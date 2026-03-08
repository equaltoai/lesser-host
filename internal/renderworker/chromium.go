package renderworker

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	slashpath "path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
)

var chromiumOnce sync.Once
var chromiumPath string
var chromiumInitErr error

func ensureChromiumReady(_ context.Context) (string, error) {
	chromiumOnce.Do(func() {
		chromiumPath, chromiumInitErr = initChromium()
	})
	return chromiumPath, chromiumInitErr
}

func initChromium() (string, error) {
	execPath := filepath.Join(os.TempDir(), "chromium")
	if fileExists(execPath) {
		setupChromiumEnv()
		return execPath, nil
	}

	binDir := strings.TrimSpace(os.Getenv("CHROMIUM_BIN_DIR"))
	if binDir == "" {
		binDir = "/opt/chromium"
	}

	if err := inflateBrotliFile(filepath.Join(binDir, "chromium.br"), execPath, 0o700); err != nil {
		return "", err
	}

	if err := inflateTarBrotli(filepath.Join(binDir, "fonts.tar.br"), filepath.Join(os.TempDir(), "fonts"), "fonts"); err != nil {
		return "", err
	}
	_ = inflateTarBrotli(filepath.Join(binDir, "swiftshader.tar.br"), os.TempDir(), "swiftshader")
	if err := inflateTarBrotli(filepath.Join(binDir, "al2023.tar.br"), filepath.Join(os.TempDir(), "al2023"), "al2023"); err != nil {
		return "", err
	}

	setupChromiumEnv()
	return execPath, nil
}

func setupChromiumEnv() {
	tmp := os.TempDir()

	if os.Getenv("FONTCONFIG_PATH") == "" {
		_ = os.Setenv("FONTCONFIG_PATH", filepath.Join(tmp, "fonts"))
	}
	if os.Getenv("HOME") == "" {
		_ = os.Setenv("HOME", tmp)
	}

	baseLib := filepath.Join(tmp, "al2023", "lib")
	ld := os.Getenv("LD_LIBRARY_PATH")
	if ld == "" {
		_ = os.Setenv("LD_LIBRARY_PATH", baseLib)
		return
	}
	if strings.HasPrefix(ld, baseLib) {
		return
	}
	_ = os.Setenv("LD_LIBRARY_PATH", baseLib+":"+ld)
}

func inflateBrotliFile(src string, dest string, mode os.FileMode) error {
	if fileExists(dest) {
		return nil
	}
	if strings.TrimSpace(src) == "" {
		return fmt.Errorf("brotli source is required")
	}

	in, err := os.Open(src) //nolint:gosec // src is controlled by Lambda configuration and points to packaged assets.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	if mkdirErr := os.MkdirAll(filepath.Dir(dest), 0o750); mkdirErr != nil {
		return mkdirErr
	}

	tmp := dest + ".tmp"
	// #nosec G304 -- tmp is derived from a controlled destination path in /tmp.
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}

	const maxBrotliInflatedBytes = 512 << 20 // 512 MiB
	r := brotli.NewReader(in)
	lr := &io.LimitedReader{R: r, N: maxBrotliInflatedBytes + 1}
	if _, err := io.Copy(out, lr); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if lr.N <= 0 {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("brotli output too large")
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func inflateTarBrotli(src string, destDir string, kind string) error {
	if strings.TrimSpace(src) == "" {
		return fmt.Errorf("tar source is required")
	}
	if strings.TrimSpace(destDir) == "" {
		return fmt.Errorf("dest dir is required")
	}

	if shouldSkipTarInflation(destDir, kind) {
		return nil
	}

	in, err := openTarSource(src, kind)
	if err != nil {
		return err
	}
	if in == nil {
		return nil
	}
	defer func() { _ = in.Close() }()

	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return err
	}

	r := brotli.NewReader(in)
	tr := tar.NewReader(r)

	limits := tarExtractLimits{
		MaxFileBytes:  128 << 20,  // 128 MiB per file
		MaxTotalBytes: 1024 << 20, // 1 GiB across the archive
	}

	if err := extractTar(tr, destDir, limits); err != nil {
		return err
	}

	// Give the filesystem a moment; this avoids rare races in Lambda warm starts.
	time.Sleep(5 * time.Millisecond)
	return nil
}

type tarExtractLimits struct {
	MaxFileBytes  int64
	MaxTotalBytes int64
}

func shouldSkipTarInflation(destDir string, kind string) bool {
	switch kind {
	case "fonts", "al2023":
		return dirExists(destDir)
	case "swiftshader":
		return fileExists(filepath.Join(destDir, "libGLESv2.so"))
	default:
		return false
	}
}

func openTarSource(src string, kind string) (*os.File, error) {
	f, err := os.Open(src) //nolint:gosec // src is controlled by Lambda configuration and points to packaged assets.
	if err != nil {
		// swiftshader is optional.
		if kind == "swiftshader" && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

func extractTar(tr *tar.Reader, destDir string, limits tarExtractLimits) error {
	if tr == nil {
		return fmt.Errorf("tar reader is nil")
	}

	var extractedBytes int64

	for {
		hdr, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		name, ok := sanitizeTarName(hdr.Name)
		if !ok {
			return fmt.Errorf("invalid tar path: %q", hdr.Name)
		}

		target, err := validateTarTarget(destDir, name, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if mkdirErr := os.MkdirAll(target, 0o750); mkdirErr != nil { //nolint:gosec // target is validated to be within destDir.
				return mkdirErr
			}
		case tar.TypeReg:
			extractedBytes, err = extractTarFile(tr, hdr, target, extractedBytes, limits)
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Ignore symlinks for safety; packaged assets should not need them.
			continue
		default:
			continue
		}
	}
}

func sanitizeTarName(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.Contains(raw, "\\") {
		return "", false
	}

	name := slashpath.Clean(raw)
	if slashpath.IsAbs(name) {
		return "", false
	}
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return "", false
	}
	name = strings.TrimPrefix(name, "./")
	if name == "" || name == "." {
		return "", false
	}
	return filepath.FromSlash(name), true
}

func validateTarTarget(destDir string, name string, rawName string) (string, error) {
	cleanDestDir := filepath.Clean(destDir)
	if strings.Contains(name, "\\") || !filepath.IsLocal(name) {
		return "", fmt.Errorf("invalid tar path: %q", rawName)
	}
	target := filepath.Join(cleanDestDir, name)
	cleanTarget := filepath.Clean(target)
	targetPrefix := cleanDestDir + string(os.PathSeparator)
	if cleanTarget != cleanDestDir && !strings.HasPrefix(cleanTarget, targetPrefix) {
		return "", fmt.Errorf("invalid tar path: %q", rawName)
	}
	return cleanTarget, nil
}

func extractTarFile(tr *tar.Reader, hdr *tar.Header, target string, extractedBytes int64, limits tarExtractLimits) (int64, error) {
	if hdr == nil {
		return extractedBytes, fmt.Errorf("tar header is nil")
	}
	if hdr.Size < 0 || (limits.MaxFileBytes > 0 && hdr.Size > limits.MaxFileBytes) {
		return extractedBytes, fmt.Errorf("tar entry too large: %q", hdr.Name)
	}

	extractedBytes += hdr.Size
	if limits.MaxTotalBytes > 0 && extractedBytes > limits.MaxTotalBytes {
		return extractedBytes, fmt.Errorf("tar output too large")
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil { //nolint:gosec // target is validated to be within destDir.
		return extractedBytes, err
	}

	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // target is validated to be within destDir.
	if err != nil {
		return extractedBytes, err
	}
	if _, err := io.CopyN(f, tr, hdr.Size); err != nil {
		_ = f.Close()
		return extractedBytes, err
	}
	if err := f.Close(); err != nil {
		return extractedBytes, err
	}
	return extractedBytes, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
