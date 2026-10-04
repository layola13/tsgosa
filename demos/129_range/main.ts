function main(): i32 {
  const a: number[] = [10, 20, 30, 40, 50];
  let t: i32 = 0;
  for (let i: i32 = 1; i < 4; i++) {
    t = t + a[i];
  }
  console.log(t);
  return 0;
}
