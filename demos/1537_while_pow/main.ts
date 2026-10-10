function main(): i32 {
  let n: i32 = 2;
  let p = 1;
  while (n > 0) { p = p * 10; n = n - 1; }
  console.log(p);
  return 0;
}
