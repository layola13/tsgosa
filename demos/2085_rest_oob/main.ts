function main(): i32 {
  const a: number[] = [1];
  const [x, y, z, ...rest] = a;
  console.log(rest.length);
  return 0;
}
