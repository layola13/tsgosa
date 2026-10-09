function f(a: i32, b: i32 = 9): i32 {
  return a + b;
}
function main(): i32 {
  console.log(f(1));
  return 0;
}
