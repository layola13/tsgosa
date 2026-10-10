function main(): i32 {
  const a: i32[] = [1, 2, 3];
  let t = 0;
  for (let i = a.length - 1; i >= 0; i--) { t += a[i]; }
  console.log(t);
  return 0;
}
