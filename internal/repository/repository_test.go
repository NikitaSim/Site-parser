package repository

import "testing"

func TestVectorLiteral(t *testing.T) {
	got := vectorLiteral([]float32{0.25, -0.5, 1})
	want := "[0.25,-0.5,1]"
	if got != want {
		t.Fatalf("vectorLiteral() = %q, want %q", got, want)
	}
}
