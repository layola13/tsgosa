function g(a: i32, b: i32 = 5): i32 {
  return a + b;
}
const f = (a: i32, b: i32 = 5): i32 => a + b;
function main(): i32 {
  console.log(g(1));
  console.log(g(1, 2));
  console.log(f(1));
  console.log(f(1, 2));
  return 0;
}
