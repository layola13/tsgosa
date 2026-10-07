function main(): i32 {
  const a: number[] = Array.of(1, 2);
  console.log(a.length);
  console.log(a[1]);
  const b: number[] = Array.of(7);
  console.log(b.length);
  console.log(b[0]);
  const d: number[] = Array.of();
  console.log(d.length);
  return 0;
}
