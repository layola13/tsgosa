function main(): i32 {
  let x = 256;
  let t = 0;
  while (x > 1) {
    x >>= 1;
    t += 1;
  }
  console.log(t);
  console.log(x);
  return 0;
}
