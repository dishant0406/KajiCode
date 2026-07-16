package longproc

import "testing"

func TestProcess(t *testing.T) {
	if got := Process(100); got != 4950 {
		t.Fatalf("Process(100) = %d, want 4950", got)
	}
}

func BenchmarkProcess(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Process(100)
	}
}
