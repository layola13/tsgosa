function main(): i32 {
  const a: number[] = [7, 8, 9];
  const f: boolean = true;
  const i: number = 2;
  if (a[i] == f) { console.log(1); } else { console.log(0); }
  if (f != a[0]) { console.log(1); } else { console.log(0); }
  return 0;
}
