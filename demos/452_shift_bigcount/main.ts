function main(): i32 {
  console.log(8 >> 33);
  console.log(1 << 33);
  console.log(-8 >> 33);
  console.log(8 >>> 33);
  let a = 8;
  a >>= 33;
  console.log(a);
  let b = 1;
  b <<= 33;
  console.log(b);
  let c = 8;
  c >>>= 33;
  console.log(c);
  return 0;
}
