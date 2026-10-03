function add3(a: i32, b: i32, c: i32): i32 {
  return a + b + c;
}
function main(): i32 {
  const p: number[] = [10, 20, 30];
  console.log(add3(...p));
  return 0;
}
