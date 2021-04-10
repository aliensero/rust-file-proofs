use std::env;
use lazy_static::lazy_static;
use sha2raw::{platform::Implementation,consts::H256};
use byteorder::{ByteOrder, BE};
use log::{debug};
lazy_static! {
    static ref IMPL: Implementation = Implementation::detect();
}

#[no_mangle]
pub unsafe extern "C" fn init(){
    fil_logger::init();
}

#[no_mangle]
pub unsafe extern "C" fn filsha256_layer(state: &mut [u32;8], len: usize, blocks: &[[u8;32];14]){
    debug!("state {:?}",state);
    let zero = [0u32;8];
    if *state == zero {
        state.copy_from_slice(&H256[..])
    }
    let l = len;
    debug!("blocks {:?} len {}",blocks,l);
    let mut tb = Vec::<&[u8]>::with_capacity(l);
    for i in 0..l{
        tb.push(&blocks[i][..]);
    }
    let mut c_state = [0u32;8];
    c_state.copy_from_slice(&state[..]);
    IMPL.compress256(&mut c_state, &tb[..]);
    state.copy_from_slice(&c_state[..]);
}

#[no_mangle]
pub unsafe extern "C"  fn finish(state: &mut [u32;8],len: u64,ret: &mut [u8; 32]) {

    debug!("state {:?}",state);

    let mut block0 = [0u8; 32];
    let mut block1 = [0u8; 32];

    // Append single 1 bit
    block0[0] = 0b1000_0000;

    // Write L as 64 big endian integer
    block1[32 - 8..].copy_from_slice(&len.to_be_bytes()[..]);

    let mut c_state = [0u32;8];
    c_state.copy_from_slice(&state[..]);
    IMPL.compress256(&mut c_state, &[&block0[..], &block1[..]][..]);

    let mut out = [0u8;32];
    BE::write_u32_into(&c_state[..], &mut out);
    out[31] &= 0b0011_1111;
    ret.copy_from_slice(&out[..]);
}

#[no_mangle]
pub unsafe extern "C"  fn finish_with(state: &mut [u32;8],len: u64,block0: &[u8;32],ret: &mut [u8; 32]) {
    
    debug!("state {:?} len {}",state,len);
    
    let mut block1 = [0u8; 32];

    // Append single 1 bit
    block1[0] = 0b1000_0000;

    // Write L as 64 big endian integer
    let l = len + 256;
    block1[32 - 8..].copy_from_slice(&l.to_be_bytes()[..]);

    let mut c_state = [0u32;8];
    c_state.copy_from_slice(&state[..]);
    debug!("len {}",l);
    IMPL.compress256(&mut c_state, &[&block0[..], &block1[..]][..]);

    let mut out = [0u8;32];
    BE::write_u32_into(&c_state[..], &mut out);
    out[31] &= 0b0011_1111;
    ret.copy_from_slice(&out[..]);
}


#[cfg(test)]
mod tests {
    #[test]
    fn it_works() {
        assert_eq!(2 + 2, 4);
    }
}
