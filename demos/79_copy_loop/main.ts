function main(): i32 {
  const a: number[] = [5, 6, 7];
  const c: number[] = [0, 0, 0];
  for (let i: i32 = 0; i < 3; i++) {
    c[i] = a[i];
  }
  console.log(c[0] + c[1] + c[2]);
  return 0;
}
