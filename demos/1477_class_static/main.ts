class M {
  static add(a: i32, b: i32): i32 {
    return a + b;
  }
}
function main(): i32 {
  console.log(M.add(2, 3));
  return 0;
}
