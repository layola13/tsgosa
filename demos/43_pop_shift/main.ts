function main(): i32 {
  const a: number[] = [1, 2, 3];
  console.log(a.pop());
  console.log(a.shift());
  a.reverse();
  console.log(a[0] + a[1]);
  return 0;
}
