function sum(a: i32, b: i32): i32 {
  return a + b;
}
function main(): i32 {
  const args: number[] = [3, 4];
  console.log(sum(...args));
  return 0;
}
