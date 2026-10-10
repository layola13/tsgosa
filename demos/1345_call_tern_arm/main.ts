function get(n: i32): i32 {
  return n * 2;
}
function main(): i32 {
  const c: i32 = 1;
  console.log(c ? get(21) : 0);
  return 0;
}
