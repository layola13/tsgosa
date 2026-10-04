function pick(a: i32, b: i32): i32 {
  if (a > b) {
    return a;
  }
  return b;
}
function main(): i32 {
  console.log(pick(7, 12), pick(9, 3));
  return 0;
}
