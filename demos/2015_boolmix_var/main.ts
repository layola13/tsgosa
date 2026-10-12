function eqn(f: boolean, n: number): number {
  return f == n ? 1 : 0;
}
function main(): i32 {
  console.log(eqn(true, 1));
  console.log(eqn(true, 2));
  console.log(eqn(false, 0));
  return 0;
}
