package atomicfile

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteReplacesFileAndSkipsUnchangedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "blueprint.yaml")
	changed, err := Write(path, []byte("first\n"), 0o640)
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	changed, err = Write(path, []byte("first\n"), 0o640)
	if err != nil || changed {
		t.Fatalf("unchanged write: changed=%v err=%v", changed, err)
	}
	changed, err = Write(path, []byte("second\n"), 0o640)
	if err != nil || !changed {
		t.Fatalf("replacement: changed=%v err=%v", changed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "second\n" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestWriteNeverExposesPartialContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blueprint.yaml")
	first := bytes.Repeat([]byte("a"), 256*1024)
	second := bytes.Repeat([]byte("b"), 256*1024)
	if _, err := Write(path, first, 0o644); err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	failures := make(chan []byte, 1)
	done := make(chan struct{})
	wait.Add(1)
	go func() {
		defer wait.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				failures <- []byte(err.Error())
				return
			}
			if !bytes.Equal(data, first) && !bytes.Equal(data, second) {
				failures <- data
				return
			}
		}
	}()
	for index := range 20 {
		content := second
		if index%2 == 1 {
			content = first
		}
		if _, err := Write(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	close(done)
	wait.Wait()
	select {
	case partial := <-failures:
		t.Fatalf("reader observed invalid content of length %d", len(partial))
	default:
	}
}
