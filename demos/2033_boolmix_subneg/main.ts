function main(): i32 {
  const a: number[] = [1];
  const f: boolean = false;
  console.log(f == a[-1]);
  console.log(f != a[9]);
  return 0;
}
