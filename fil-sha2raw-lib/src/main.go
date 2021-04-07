package main

/*
#cgo LDFLAGS: -L/root/alien-rust-test/target/release -lfilsha256 -ldl
#include <stdlib.h>
void filsha256_layer(u_int32_t* state, u_int64_t len, u_char** blocks);
void finish(u_int32_t* state,u_int64_t len, u_char* ret);
void finish_with(u_int32_t* state, u_int64_t len, u_char* block0, u_char* ret);
*/
import "C"
import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"unsafe"
)

func main() {
	//var state []uint32 = make([]uint32, 8)
	var t [8]uint32
	fmt.Println("t", t)

	var pb byte = 9
	provider_id := make([]byte, 32)
	for i := 0; i < 32; i++ {
		provider_id[i] = pb
	}
	var tb byte = 1
	ticket := make([]byte, 32)
	for i := 0; i < 32; i++ {
		ticket[i] = tb
	}
	commd := []byte{252, 126, 146, 130, 150, 229, 22, 250, 173, 233, 134, 178, 143, 146, 212, 74, 79, 36, 185, 53, 72, 82, 35, 55, 106, 121, 144, 39, 188, 24, 248, 51}
	porepseed := []byte{99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99}
	sector_id := []byte{0, 0, 0, 0, 0, 0, 0, 0}
	fmt.Println("provider_id", provider_id)
	fmt.Println("sector_id", sector_id)
	fmt.Println("ticket", ticket)
	fmt.Println("commd", commd)
	fmt.Println("porepseed", porepseed)
	sha := sha256.New()
	sha.Write(provider_id)
	sha.Write(sector_id)
	sha.Write(ticket)
	sha.Write(commd)
	sha.Write(porepseed)
	rid := sha.Sum(nil)
	fmt.Println("rid", rid)

	var layerIndex uint32 = 1
	var node uint64 = 0
	lbuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lbuf, layerIndex)
	nbuf := make([]byte, 8)
	binary.BigEndian.PutUint64(nbuf, node)
	fmt.Println("lbuf", lbuf)
	fmt.Println("nbuf", nbuf)
	res := make([]byte, 20)
	for i := 0; i < 20; i++ {
		res[i] = 0
	}
	b1 := make([]byte, 0, 32)
	b1 = append(b1, lbuf...)
	b1 = append(b1, nbuf...)
	b1 = append(b1, res...)
	var b0 [32]byte
	copy(b0[:], rid[:32])
	var b01 [32]byte
	copy(b01[:], b1[:])
	var bls [14][32]byte = [14][32]byte{b0, b01}

	t = GoShaLayer(t, 2, bls)
	ret := GoFinish(t, uint64(2<<8))
	for i := 0; i < 6; i++ {
		bls[i] = ret
	}
	t = [8]uint32{}
	node = 1
	binary.BigEndian.PutUint64(nbuf, node)
	b1 = make([]byte, 0, 32)
	b1 = append(b1, lbuf...)
	b1 = append(b1, nbuf...)
	b1 = append(b1, res...)
	copy(b0[:], rid[:32])
	copy(b01[:], b1[:])
	var bls1 [14][32]byte = [14][32]byte{b0, b01}
	t = GoShaLayer(t, 2, bls1)
	for i := 0; i < 6; i++ {
		t = GoShaLayer(t, 6, bls)
	}
	ret = GoFinishWith(t, uint64(2<<8+6<<8*6), bls[0])
	fmt.Println("finish", ret)
}

func GoShaLayer(gostate [8]uint32, len uint64, goblocks [14][32]byte) [8]uint32 {
	var state *C.u_int32_t = (*C.u_int32_t)(unsafe.Pointer(&gostate[0]))
	var blocks **C.uchar = (**C.uchar)(unsafe.Pointer(&goblocks[0][0]))
	C.filsha256_layer(state, C.ulong(len), blocks)
	return gostate
}

func GoFinish(gostate [8]uint32, len uint64) [32]byte {
	var state *C.u_int32_t = (*C.u_int32_t)(unsafe.Pointer(&gostate[0]))
	c_len := C.ulong(len)
	var ret [32]byte
	c_ret := (*C.uchar)(unsafe.Pointer(&ret[0]))
	C.finish(state, c_len, c_ret)
	return ret
}

func GoFinishWith(gostate [8]uint32, len uint64, block0 [32]byte) [32]byte {
	var state *C.u_int32_t = (*C.u_int32_t)(unsafe.Pointer(&gostate[0]))
	c_len := C.ulong(len)
	var ret [32]byte
	c_ret := (*C.uchar)(unsafe.Pointer(&ret[0]))
	c_b0 := (*C.uchar)(unsafe.Pointer(&block0[0]))
	C.finish_with(state, c_len, c_b0, c_ret)
	return ret
}
