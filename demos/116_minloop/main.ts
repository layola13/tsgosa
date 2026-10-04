function main(): i32 {
  const a: number[] = [8, 3, 11, 2, 9];
  let m: i32 = a[0];
  for (let i: i32 = 1; i < a.length; i++) {
    if (a[i] < m) {
      m = a[i];
    }
  }
  console.log(m);
  return 0;
}
