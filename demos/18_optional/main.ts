function main(): i32 {
  const a: number[] = [10, 20, 30];
  const v = a?.[1] ?? -1;
  console.log(v);
  console.log(a.length);
  return 0;
}
