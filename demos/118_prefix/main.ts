function main(): i32 {
  const a: number[] = [2, 4, 6];
  let t: i32 = 0;
  for (let i: i32 = 0; i < a.length; i++) {
    t = t + a[i];
    a[i] = t;
  }
  console.log(a[0], a[1], a[2]);
  return 0;
}
