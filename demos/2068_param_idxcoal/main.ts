function nth(a: number[], i: number): number {
  return a[i] ?? -1;
}
function main(): i32 {
  console.log(nth([7, 8], 0));
  console.log(nth([7, 8], 5));
  return 0;
}
