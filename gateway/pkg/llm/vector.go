package llm

import (
	"encoding/binary"
	"math"
)

// EncodeFloat32Vector 将 []float32 序列化为字节序列（小端 raw，4 字节/维）。
// 用于记忆 embedding 的 BLOB 存储（AR-09 记忆升级）。
func EncodeFloat32Vector(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// DecodeFloat32Vector 将字节序列反序列化为 []float32；长度为 0 或非 4 的倍数时返回 nil。
func DecodeFloat32Vector(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// CosineSimilarity 计算两个向量的余弦相似度，取值 [-1,1]。
// 维度不一致、空向量或零向量时返回 0（视为不相关）。
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
