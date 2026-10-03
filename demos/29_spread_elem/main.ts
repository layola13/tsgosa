function main(): i32 {
  const a: number[] = [1, 2, 3];
  const c = [...a, 4];
  console.log(c.length + c[3]);
  return 0;
}
