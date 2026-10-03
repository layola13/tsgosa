function main(): i32 {
  const a: number[] = [7, 2, 9, 4];
  let m: i32 = a[0];
  for (const x of a) {
    if (x > m) {
      m = x;
    }
  }
  console.log(m);
  return 0;
}
