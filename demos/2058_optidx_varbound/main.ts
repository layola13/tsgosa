function main(): i32 {
  const a: number[] = [1, 2, 3];
  const i: number = 5;
  console.log(a?.[i] ?? -1);
  return 0;
}
