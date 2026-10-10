class C {
  static K = 0;
  static {
    C.K = 42;
  }
}
function main(): i32 {
  console.log(C.K);
  return 0;
}
