function main(): i32 {
  const a: number[] = [1, 2, 3, 4, 5, 6, 7, 8];
  let n: i32 = 0;
  for (const x of a) {
    if (x % 2 == 0) {
      n = n + 1;
    }
  }
  console.log(n);
  return 0;
}