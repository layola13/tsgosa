class C {
  static add(a: i32, b: i32): i32 { return a + b; }
}
function main(): i32 {
  console.log(C.add(3, 4));
  return 0;
}
