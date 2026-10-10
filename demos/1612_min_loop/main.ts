function main(): i32 {
  const a: i32[] = [4, 2, 8, 1];
  let m = a[0];
  for (let i = 1; i < a.length; i = i + 1) { if (a[i] < m) { m = a[i]; } }
  console.log(m);
  return 0;
}
