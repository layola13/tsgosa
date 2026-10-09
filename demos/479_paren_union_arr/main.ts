function main(): i32 {
  const a: (number | null)[] = [1, null, 3];
  console.log(a[0]);
  console.log(a.length);
  const b: Array<number | undefined> = [4, undefined];
  console.log(b.length);
  return 0;
}
