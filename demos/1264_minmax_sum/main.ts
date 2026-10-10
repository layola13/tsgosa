function min(a: i32, b: i32): i32 { return a < b ? a : b; }
function max(a: i32, b: i32): i32 { return a > b ? a : b; }
function main(): i32 {
  console.log(min(3, 7) + max(3, 7));
  return 0;
}
