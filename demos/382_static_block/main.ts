class C {
  static n: i32 = 1;
  static {
    C.n = 5;
  }
}
function main(): i32 {
  console.log(C.n);
  return 0;
}
