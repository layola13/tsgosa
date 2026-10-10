function main(): i32 {
  let a = 5;
  let b = 9;
  a ^= b;
  b ^= a;
  a ^= b;
  console.log(a);
  console.log(b);
  return 0;
}
