function main(): i32 {
  let x: i32 = 29;
  let n: i32 = 0;
  while (x != 0) {
    n = n + (x & 1);
    x = x >> 1;
  }
  console.log(n);
  return 0;
}