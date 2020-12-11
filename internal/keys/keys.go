package keys

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
)

// Functions related to Kademlia key / node IDs

const KeySize = 16 // The size of a key in bytes (I use 128-bit keys)

type Key = [KeySize]byte

func Format(k []byte) string {
	return hex.EncodeToString(k[:])
}

func Random() Key {
	var id Key
	rand.Read(id[:])
	return id
}

func Distance(k1 *Key, k2 *Key) *Key {
	var distance Key
	for i := 0; i < KeySize; i++ {
		distance[i] = k1[i] ^ k2[i]
	}
	return &distance
}

func Bit(k *Key, i int) byte {
	o := i / 8
	b := i % 8
	return (k[o] >> b) & 1
}

func SetBit(k *Key, i int, v byte) [KeySize]byte {
	var newK Key
	copy(newK[:], k[:])

	o := i / 8
	b := i % 8
	newK[o] = (newK[o] &^ (1 << b)) | ((v & 1) << b)

	return newK
}

// k1 includes k2 if the first l bits of k1 and k2 are equal
func Includes(k1 *Key, k2 *[KeySize]byte, l int) bool {
	o := 0
	var b byte = 1
	for i := 0; i < l; i++ {
		if k1[o]&b != k2[o]&b {
			return false
		}

		if b == 1<<7 {
			o++
			b = 1
		}
	}
	return true
}

// 1 if k1 > k2, 0 if k1 == k2, -1 if k1 < k2
func Compare(k1 *Key, k2 *Key) int {
	return bytes.Compare(k1[:], k2[:])
}

func Le(k1 *Key, k2 *Key) bool {
	return Compare(k1, k2) == -1
}

func Leq(k1 *Key, k2 *Key) bool {
	return Compare(k1, k2) <= 0
}

func Eq(k1 *Key, k2 *Key) bool {
	return Compare(k1, k2) == 0
}

func Geq(k1 *Key, k2 *Key) bool {
	return Compare(k1, k2) >= 0
}

func Gr(k1 *Key, k2 *Key) bool {
	return Compare(k1, k2) == 1
}
