function main(): i32 {
  const a: i32[] = [1, 2, 3, 4];
  let p = 1;
  for (let i = 0; i < a.length; i = i + 1) { p = p * a[i]; }
  console.log(p);
  return 0;
}
