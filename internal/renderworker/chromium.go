package renderworker

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}

	r := brotli.NewReader(in)
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
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

	switch kind {
	case "fonts":
		if dirExists(destDir) {
			return nil
		}
	case "al2023":
		if dirExists(destDir) {
			return nil
		}
	case "swiftshader":
		if fileExists(filepath.Join(destDir, "libGLESv2.so")) {
			return nil
		}
	}

	in, err := os.Open(src)
	if err != nil {
		// swiftshader is optional.
		if kind == "swiftshader" && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	r := brotli.NewReader(in)
	tr := tar.NewReader(r)

	for {
		hdr, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}

		name := filepath.Clean(hdr.Name)
		name = strings.TrimPrefix(name, "/")
		if name == "" || name == "." {
			continue
		}

		target := filepath.Join(destDir, name)
		if !strings.HasPrefix(target, destDir+string(os.PathSeparator)) && target != destDir {
			return fmt.Errorf("invalid tar path: %q", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}

			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Ignore symlinks for safety; packaged assets should not need them.
			continue
		default:
			continue
		}
	}

	// Give the filesystem a moment; this avoids rare races in Lambda warm starts.
	time.Sleep(5 * time.Millisecond)
	return nil
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
