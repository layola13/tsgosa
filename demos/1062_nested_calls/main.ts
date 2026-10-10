function add(a: i32, b: i32): i32 { return a + b; }
function mul(a: i32, b: i32): i32 { return a * b; }
function main(): i32 {
  console.log(add(mul(2, 3), mul(4, 5)));
  return 0;
}
