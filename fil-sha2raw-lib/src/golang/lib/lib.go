package lib

/*
#cgo LDFLAGS: -L../../../../target/release -lfilsha256 -ldl
#include <stdlib.h>
void init();
void filsha256_layer(u_int32_t* state, u_int64_t len, u_char** blocks);
void finish(u_int32_t* state,u_int64_t len, u_char* ret);
void finish_with(u_int32_t* state, u_int64_t len, u_char* block0, u_char* ret);
*/
import "C"
import (
	"unsafe"
)

func init() {
	C.init()
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
