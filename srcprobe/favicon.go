package srcprobe

import (
	"encoding/base64"
	"encoding/binary"
)

// faviconHash 计算 FOFA 口径的 favicon 哈希：mmh3(base64(icon)) 的有符号 32 位值。
// 与常见脚本（Python mmh3.hash(b64, signed=True)）一致，可直接拿去和测绘结果对照。
func faviconHash(icon []byte) int32 {
	b64 := base64.StdEncoding.EncodeToString(icon)
	return int32(murmur3_32([]byte(b64), 0))
}

// murmur3_32 是 MurmurHash3 x86 32-bit（seed=0）。
func murmur3_32(data []byte, seed uint32) uint32 {
	const (
		c1 = 0xcc9e2d51
		c2 = 0x1b873593
	)
	h1 := seed
	nblocks := len(data) / 4
	for i := 0; i < nblocks; i++ {
		k1 := binary.LittleEndian.Uint32(data[i*4:])
		k1 *= c1
		k1 = (k1 << 15) | (k1 >> 17)
		k1 *= c2
		h1 ^= k1
		h1 = (h1 << 13) | (h1 >> 19)
		h1 = h1*5 + 0xe6546b64
	}
	tail := data[nblocks*4:]
	var k1 uint32
	switch len(tail) {
	case 3:
		k1 ^= uint32(tail[2]) << 16
		fallthrough
	case 2:
		k1 ^= uint32(tail[1]) << 8
		fallthrough
	case 1:
		k1 ^= uint32(tail[0])
		k1 *= c1
		k1 = (k1 << 15) | (k1 >> 17)
		k1 *= c2
		h1 ^= k1
	}
	h1 ^= uint32(len(data))
	h1 ^= h1 >> 16
	h1 *= 0x85ebca6b
	h1 ^= h1 >> 13
	h1 *= 0xc2b2ae35
	h1 ^= h1 >> 16
	return h1
}
