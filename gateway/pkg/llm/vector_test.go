package llm

import (
	"math"
	"testing"
)

func TestVector_Codec(t *testing.T) {
	v := []float32{1.0, 2.5, -3.0}
	b := EncodeFloat32Vector(v)
	if len(b) != 12 {
		t.Fatalf("encoded len = %d, want 12", len(b))
	}
	got := DecodeFloat32Vector(b)
	if len(got) != 3 || got[0] != 1.0 || got[1] != 2.5 || got[2] != -3.0 {
		t.Fatalf("decoded = %v", got)
	}
	// nil / 奇数长度 -> nil
	if DecodeFloat32Vector(nil) != nil {
		t.Fatal("decode(nil) should be nil")
	}
	if DecodeFloat32Vector([]byte{1, 2, 3}) != nil {
		t.Fatal("decode(odd) should be nil")
	}
}

func TestVector_CosineSimilarity(t *testing.T) {
	// 相同向量 -> 1.0
	c := CosineSimilarity([]float32{1, 2, 3}, []float32{1, 2, 3})
	if math.Abs(c-1.0) > 1e-6 {
		t.Fatalf("identical cosine = %v, want 1.0", c)
	}
	// 正交向量 -> 0
	c = CosineSimilarity([]float32{1, 0}, []float32{0, 1})
	if math.Abs(c) > 1e-6 {
		t.Fatalf("orthogonal cosine = %v, want 0", c)
	}
	// 维度不一致 -> 0
	c = CosineSimilarity([]float32{1, 0, 0}, []float32{1, 0})
	if c != 0 {
		t.Fatalf("mismatched-dim cosine = %v, want 0", c)
	}
	// 零向量 -> 0
	c = CosineSimilarity([]float32{0, 0}, []float32{1, 1})
	if c != 0 {
		t.Fatalf("zero-vector cosine = %v, want 0", c)
	}
	// 空向量 -> 0
	c = CosineSimilarity([]float32{}, []float32{})
	if c != 0 {
		t.Fatalf("empty cosine = %v, want 0", c)
	}
}
