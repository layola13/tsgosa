function main(): i32 {
  let a = 12;
  a &= 10;
  console.log(a);
  let b = 12;
  b |= 10;
  console.log(b);
  let c = 12;
  c ^= 10;
  console.log(c);
  return 0;
}
