function main(): i32 {
  const a: number[] = [5];
  a[0]--;
  console.log(a[0]);
  let s: number = 0;
  for (let i = 0; i < 3; i = i + 1) { s = s + a[0]; }
  console.log(s);
  return 0;
}
