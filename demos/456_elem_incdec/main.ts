function main(): i32 {
  const a: number[] = [1, 10];
  a[0]++;
  console.log(a[0]);
  console.log(a[0]++);
  console.log(++a[1]);
  a[1]--;
  console.log(a[1]);
  return 0;
}
