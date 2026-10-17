package mmap

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

func rssKiB() int {
	b, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			var k int
			fmt.Sscanf(line, "VmRSS: %d kB", &k)
			return k
		}
	}
	return 0
}

func TestIssueMmapRelease(t *testing.T) {
	runtime.GC()
	before := rssKiB()

	for k := 0; k < 32; k++ {
		m := AllocEmbeddingMatrix(1024, 1024)

		rows := m.Rows()
		rows[0][0] = 1
		runtime.KeepAlive(rows)

		if err := m.Close(); err != nil { // ← explicit release
			t.Fatal(err)
		}
	}

	runtime.GC()
	runtime.GC()
	delta := rssKiB() - before
	t.Logf("RSS delta after explicit Close: %d KiB", delta)
	if delta > 64*1024 {
		t.Fatal("anonymous mappings remain allocated")
	}
}
