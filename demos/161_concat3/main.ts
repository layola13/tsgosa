function main(): i32 {
  const c: number[] = [1].concat([2], [3, 4]);
  console.log(c.length, c[0], c[3]);
  return 0;
}