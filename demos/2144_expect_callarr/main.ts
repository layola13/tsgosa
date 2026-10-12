function double(a: number[]): number[] {
  const r: number[] = [a[0] * 2, a[1] * 2];
  return r;
}
function main(): i32 {
  expect(double([1, 2])).toStrictEqual([2, 4]);
  console.log(1);
  return 0;
}
