package atomicfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

func Write(path string, data []byte, mode os.FileMode) (bool, error) {
	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, data) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read current file: %w", err)
	}

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return false, fmt.Errorf("create output directory: %w", err)
	}

	temporary, err := os.CreateTemp(directory, ".blueprint-*")
	if err != nil {
		return false, fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return false, fmt.Errorf("set temporary file mode: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return false, fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return false, fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return false, fmt.Errorf("replace output file: %w", err)
	}

	directoryHandle, err := os.Open(directory)
	if err != nil {
		return false, fmt.Errorf("open output directory: %w", err)
	}
	defer directoryHandle.Close()
	if err := directoryHandle.Sync(); err != nil {
		return false, fmt.Errorf("sync output directory: %w", err)
	}
	return true, nil
}
