function main(): i32 {
  const a: i32[] = [10, 20, 30];
  let t: i32 = 0;
  for (const k in a) {
    t = t + a[k];
  }
  console.log(t);
  return 0;
}
