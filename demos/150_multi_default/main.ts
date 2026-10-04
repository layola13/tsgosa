function f(a: i32 = 1, b: i32 = 2, c: i32 = 3): i32 {
  return a + b + c;
}
function main(): i32 {
  console.log(f(), f(10), f(10, 20, 30));
  return 0;
}