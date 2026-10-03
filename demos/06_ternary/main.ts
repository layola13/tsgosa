function pick(c: i32): i32 {
  return c > 10 ? 100 : 200;
}
function main(): i32 {
  console.log(pick(7));
  console.log(pick(42));
  return 0;
}
