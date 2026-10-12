function main(): i32 {
  const a: number[] = [1];
  const f: boolean = false;
  const t: boolean = true;
  console.log(f == a[0]);
  console.log(f == a[5]);
  console.log(t == a[0]);
  console.log(t != a[5]);
  return 0;
}
