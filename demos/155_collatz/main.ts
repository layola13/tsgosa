function main(): i32 {
  let x: i32 = 27;
  let n: i32 = 0;
  while (x != 1) {
    if (x % 2 == 0) {
      x = x / 2;
    } else {
      x = 3 * x + 1;
    }
    n = n + 1;
  }
  console.log(n);
  return 0;
}