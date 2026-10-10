namespace M {
  export function add(a: i32, b: i32): i32 { return a + b; }
  export function mul(a: i32, b: i32): i32 { return a * b; }
}
function main(): i32 {
  console.log(M.add(1, 2) + M.mul(3, 4));
  return 0;
}
