function main(): i32 {
  const a: i32[] = [10, 20, 30];
  let t = 0;
  for (const i in a) { t += a[i]; }
  console.log(t);
  return 0;
}
