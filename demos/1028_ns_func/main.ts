namespace M {
  export function add(a: i32, b: i32): i32 { return a + b; }
  export const K = 10;
}
function main(): i32 {
  console.log(M.add(3, 4));
  console.log(M.K);
  return 0;
}
