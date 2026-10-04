function main(): i32 {
  const a: number[] = [1, 2, 3, 4];
  a.copyWithin(0, 2);
  console.log(a[0], a[1], a[2], a[3]);
  return 0;
}
