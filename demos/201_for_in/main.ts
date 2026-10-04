function main(): i32 {
  const a: number[] = [10, 20, 30];
  let t: i32 = 0;
  for (const i in a) {
    t = t + a[i];
  }
  console.log(t);
  return 0;
}