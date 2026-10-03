function main(): i32 {
  const a: number[] = [3, 1, 4, 1, 5];
  const f = a.find((x) => x > 3);
  console.log(f);
  console.log(a.findIndex((x) => x == 1));
  console.log(a.some((x) => x > 4));
  console.log(a.every((x) => x > 0));
  return 0;
}
