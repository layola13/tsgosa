function main(): i32 {
  const a: number[] = [1, 2, 3];
  const b: number[] = [4, 5, 6];
  let t: i32 = 0;
  for (let i: i32 = 0; i < a.length; i++) {
    t = t + a[i] * b[i];
  }
  console.log(t);
  return 0;
}