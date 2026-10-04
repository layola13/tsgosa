function main(): i32 {
  let n: i32 = 0;
  for (let i: i32 = 1; i <= 20; i++) {
    if (i % 3 == 0 || i % 5 == 0) {
      n = n + 1;
    }
  }
  console.log(n);
  return 0;
}